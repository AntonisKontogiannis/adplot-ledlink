// Command ledlink runs the adplot LED Link service: a vendor-neutral gateway
// between the adplot digital signage platform and LED screen controllers
// (Huidu, Onbon, ...) reached over WiFi or Ethernet.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"adplot/ledlink/internal/api"
	"adplot/ledlink/internal/core"
	"adplot/ledlink/internal/discovery"
	"adplot/ledlink/internal/drivers/colorlight"
	"adplot/ledlink/internal/drivers/ddp"
	"adplot/ledlink/internal/drivers/huidu"
	"adplot/ledlink/internal/drivers/mock"
	"adplot/ledlink/internal/drivers/reffw"
)

// version is the LED Link release version.
const version = "0.3.0"

func main() {
	args := os.Args[1:]

	var err error
	if len(args) == 0 {
		// No arguments is the everyday-user path: start the gateway and open
		// the browser interface, so double-clicking the binary just works.
		err = runServe([]string{"-open"})
	} else {
		switch args[0] {
		case "serve":
			err = runServe(args[1:])
		case "ui":
			err = runServe(append([]string{"-open"}, args[1:]...))
		case "scan":
			err = runScan(args[1:])
		case "identify":
			err = runIdentify(args[1:])
		case "drivers":
			runDrivers()
		case "version", "--version", "-v":
			fmt.Println("ledlink", version)
		case "help", "--help", "-h":
			usage()
		default:
			fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
			usage()
			os.Exit(2)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `ledlink %s - vendor-neutral LED screen controller gateway for adplot

Usage:
  ledlink                    start the service and open the browser interface
  ledlink ui [flags]         same as above (explicit form)
  ledlink serve [flags]      run the REST API + web interface
  ledlink scan [flags]       probe the network for controllers
  ledlink identify [flags]   ask every adapter what is at this address
  ledlink drivers            list registered driver adapters
  ledlink version            print the version

Serve flags:
  -listen string        API listen address (default "127.0.0.1:8787")
  -mock-devices int     pre-connect N simulated devices (for development)
  -open                 open the browser interface after starting

Scan flags:
  -driver string        adapter to probe with: auto|huidu|mock (default "auto")
  -targets string       "local" (default), comma list of hosts, or a CIDR range
  -timeout int          per-probe timeout in milliseconds (default 800)
  -json                 print results as JSON

Identify flags:
  -timeout int          per-driver probe timeout in milliseconds (default 2000)
`, version)
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	listen := fs.String("listen", "127.0.0.1:8787", "API listen address")
	mockDevices := fs.Int("mock-devices", 0, "pre-connect N simulated mock devices")
	openUI := fs.Bool("open", false, "open the browser interface after starting")
	if err := fs.Parse(args); err != nil {
		return err
	}

	logger := log.New(os.Stderr, "", log.LstdFlags)
	reg := newRegistry()
	mgr := core.NewManager(reg)

	for i := 1; i <= *mockDevices; i++ {
		addr := fmt.Sprintf("mock-%d", i)
		if _, err := mgr.Connect(context.Background(), mock.DriverName, addr); err != nil {
			logger.Printf("mock connect %s: %v", addr, err)
			continue
		}
		logger.Printf("mock device %s connected", addr)
	}

	// Bind before announcing so the printed URL is already accepting traffic.
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}

	uiURL := browserURL(*listen)
	httpServer := &http.Server{
		Handler:           api.New(mgr, version, logger).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	logger.Printf("ledlink %s listening on %s (drivers: %v)", version, uiURL, reg.Names())

	if *openUI {
		logger.Printf("opening the control interface in your browser")
		go func() {
			if err := openBrowser(uiURL); err != nil {
				logger.Printf("could not open a browser automatically: %v (open %s manually)", err, uiURL)
			}
		}()
	}
	return httpServer.Serve(ln)
}

// browserURL turns a listen address into a URL a local browser can reach.
func browserURL(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "http://" + listen + "/"
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/"
}

// openBrowser launches the platform's default browser at url.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

func runScan(args []string) error {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	driverName := fs.String("driver", "auto", `adapter: "auto" (default, probes all adapters), "colorlight", "ddp", "huidu", "mock" or "reffw"`)
	targetsSpec := fs.String("targets", "local", `scan targets: "local", comma list of hosts, or CIDR`)
	timeoutMS := fs.Int("timeout", 800, "per-probe timeout in milliseconds")
	asJSON := fs.Bool("json", false, "print results as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	mgr := core.NewManager(newRegistry())

	targets, err := discovery.Expand(*targetsSpec, discovery.DefaultMaxHosts)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return fmt.Errorf("no scan targets found for %q", *targetsSpec)
	}

	auto := *driverName == "" || *driverName == "auto" || *driverName == "all"
	if auto {
		fmt.Fprintf(os.Stderr, "probing %d target(s) with all drivers...\n", len(targets))
	} else {
		fmt.Fprintf(os.Stderr, "probing %d target(s) with driver %q...\n", len(targets), *driverName)
	}

	var results []core.ScanResult
	if auto {
		results, err = mgr.ScanAll(context.Background(), targets, time.Duration(*timeoutMS)*time.Millisecond)
	} else {
		results, err = mgr.Scan(context.Background(), *driverName, targets, time.Duration(*timeoutMS)*time.Millisecond)
	}
	if err != nil {
		return err
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(results)
	}
	if len(results) == 0 {
		fmt.Println("no devices found")
		return nil
	}
	for _, res := range results {
		fmt.Printf("%-20s %-6s %-10s %s\n", res.Address, res.Driver, res.Protocol, res.Detail)
	}
	return nil
}

func runIdentify(args []string) error {
	fs := flag.NewFlagSet("identify", flag.ExitOnError)
	timeoutMS := fs.Int("timeout", 2000, "per-driver probe timeout in milliseconds")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: ledlink identify [-timeout ms] <address>")
	}
	addr := fs.Arg(0)

	mgr := core.NewManager(newRegistry())
	results := mgr.Identify(context.Background(), addr, time.Duration(*timeoutMS)*time.Millisecond)
	if len(results) == 0 {
		fmt.Println("no drivers registered")
		return nil
	}

	fmt.Printf("probing %s with %d driver(s):\n", addr, len(results))
	for _, res := range results {
		status := "no"
		if res.Reachable {
			status = "YES"
		}
		fmt.Printf("  %-6s reachable=%-3s protocol=%-10s %s\n", res.Driver, status, res.Protocol, res.Detail)
	}
	return nil
}

func runDrivers() {
	for _, name := range newRegistry().Names() {
		fmt.Println(name)
	}
}

// newRegistry builds the driver registry with all built-in adapters.
func newRegistry() *core.Registry {
	reg := core.NewRegistry()
	reg.Register(colorlight.New())
	reg.Register(ddp.New())
	reg.Register(huidu.New())
	reg.Register(mock.New())
	reg.Register(reffw.New())
	return reg
}
