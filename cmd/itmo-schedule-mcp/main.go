package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/prudnik-web/itmo-schedule-mcp/internal/itmo"
	"github.com/prudnik-web/itmo-schedule-mcp/internal/mcpserver"
	"github.com/prudnik-web/itmo-schedule-mcp/internal/session"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "verify-itmo" {
		if err := verify(); err != nil {
			fmt.Fprintln(os.Stderr, itmo.PublicError(err))
			os.Exit(1)
		}
		return
	}
	if len(os.Args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: itmo-schedule-mcp [verify-itmo]")
		os.Exit(2)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("server stopped", "error", err.Error())
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	listen := env("ITMO_LISTEN", "127.0.0.1:3700")
	host, port, err := net.SplitHostPort(listen)
	ip := net.ParseIP(host)
	portNumber, portErr := strconv.Atoi(port)
	if err != nil || ip == nil || !ip.IsLoopback() || portErr != nil || portNumber < 1 || portNumber > 65535 {
		return errors.New("ITMO_LISTEN must use a loopback IP and valid port")
	}
	cacheSize, err := integer("ITMO_CACHE_SIZE", 256, 10000)
	if err != nil {
		return err
	}
	concurrency, err := integer("ITMO_CONCURRENCY", 32, 256)
	if err != nil {
		return err
	}
	rpm, err := integer("ITMO_REQUESTS_PER_MINUTE", 120, 10000)
	if err != nil {
		return err
	}
	idle, err := duration("ITMO_CACHE_IDLE", 24*time.Hour, 7*24*time.Hour)
	if err != nil {
		return err
	}
	timeout, err := duration("ITMO_UPSTREAM_TIMEOUT", 12*time.Second, 20*time.Second)
	if err != nil {
		return err
	}
	cache := session.New(itmo.NewClient(timeout), cacheSize, idle)
	handler, err := mcpserver.New(cache, mcpserver.Config{
		PublicOrigin: env("ITMO_PUBLIC_ORIGIN", "https://itmo.example.com"), LocalAddress: listen,
		Concurrency: concurrency, RequestsPerMinute: rpm, RequestTimeout: 45 * time.Second, Logger: logger,
	})
	if err != nil {
		return errors.New("invalid server configuration")
	}
	server := &http.Server{
		Addr: listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 50 * time.Second, IdleTimeout: 60 * time.Second,
		MaxHeaderBytes: 16 << 10,
		// Panic/network diagnostics can contain requests. Application logs above
		// deliberately emit only fixed operational categories.
		ErrorLog: log.New(io.Discard, "", 0),
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return errors.New("cannot bind server listener")
	}
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	logger.Info("server started")
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			cache.Sweep()
		case err := <-finished:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return errors.New("HTTP server failed")
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 50*time.Second)
			defer cancel()
			if err := server.Shutdown(shutdown); err != nil {
				_ = server.Close()
				return errors.New("shutdown timed out")
			}
			logger.Info("server stopped")
			return nil
		}
	}
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
func integer(name string, fallback, maximum int) (int, error) {
	value, err := strconv.Atoi(env(name, strconv.Itoa(fallback)))
	if err != nil || value < 1 || value > maximum {
		return 0, fmt.Errorf("invalid %s", name)
	}
	return value, nil
}
func duration(name string, fallback, maximum time.Duration) (time.Duration, error) {
	value, err := time.ParseDuration(env(name, fallback.String()))
	if err != nil || value < time.Second || value > maximum {
		return 0, fmt.Errorf("invalid %s", name)
	}
	return value, nil
}

// Private SSH-only verification. Credentials arrive on stdin; results contain
// only counts and categories. No schedule or tokens are persisted or printed.
func verify() (verifyErr error) {
	report := map[string]any{}
	diagnostics := []itmo.Diagnostic{}
	defer func() {
		report["diagnostics"] = diagnostics
		if verifyErr != nil {
			report["error"] = itmo.PublicError(verifyErr)
		}
		_ = json.NewEncoder(os.Stdout).Encode(report)
	}()
	var credentials itmo.Credentials
	if json.NewDecoder(io.LimitReader(os.Stdin, 8192)).Decode(&credentials) != nil {
		return itmo.MissingCredentials
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client := itmo.NewClient(12 * time.Second)
	client.ObserveDiagnostics(func(d itmo.Diagnostic) { diagnostics = append(diagnostics, d) })
	tokens, err := client.Login(ctx, credentials)
	if err != nil {
		return err
	}
	report["login"] = "ok"
	refreshed, err := client.Refresh(ctx, tokens.Refresh)
	if err != nil {
		return err
	}
	report["refresh"] = "ok"
	dates, _ := itmo.Dates("", "", time.Now())
	schedule, err := client.Schedule(ctx, refreshed.Access, dates)
	if err != nil {
		return err
	}
	lessons := 0
	for _, day := range schedule.Days {
		lessons += len(day.Lessons)
	}
	report["schedule"], report["days"], report["lessons"] = "ok", len(schedule.Days), lessons
	return nil
}
