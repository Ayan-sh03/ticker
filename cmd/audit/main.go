// Command audit checks booking invariants across the whole database and exits
// with status 1 when any are violated. It only reads; it never repairs data.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"ticker/internal/audit"
	"ticker/internal/platform/config"
	"ticker/internal/platform/database"
)

func main() {
	failed, err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "audit error:", err)
		os.Exit(2)
	}
	if failed {
		os.Exit(1)
	}
}

func run() (bool, error) {
	var options audit.Options
	flag.BoolVar(&options.Worker, "worker", true, "a worker releases overdue holds; use false for on-demand release (Vercel)")
	flag.DurationVar(&options.HoldGrace, "hold-grace", 10*time.Second, "how long past its deadline a hold may wait for the worker")
	flag.Parse()
	if flag.NArg() != 0 || options.HoldGrace < 0 {
		return false, errors.New("usage: audit [--worker=false] [--hold-grace 10s]")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cfg, err := config.Load()
	if err != nil {
		return false, err
	}
	pool, err := database.Open(ctx, cfg)
	if err != nil {
		return false, err
	}
	defer pool.Close()
	report, err := audit.Run(ctx, pool, options)
	if err != nil {
		return false, err
	}
	printReport(report)
	return report.Failed(), nil
}

func printReport(r audit.Report) {
	fmt.Printf("Audit at %s: %d reservations, %d bookings.\n", r.At.UTC().Format(time.RFC3339), r.Reservations, r.Bookings)
	if r.Untracked > 0 {
		fmt.Printf("%d reservations predate the audit trail; their history is not checked.\n", r.Untracked)
	}
	fmt.Println()
	failed := 0
	for _, f := range r.Findings {
		if f.Count == 0 {
			fmt.Printf("  ok    %-30s %s\n", f.Name, f.Rule)
			continue
		}
		failed++
		fmt.Printf("  FAIL  %-30s %s\n", f.Name, f.Rule)
		fmt.Printf("        %d found, e.g. %s\n", f.Count, strings.Join(f.Examples, ", "))
	}
	for _, c := range r.Skipped {
		fmt.Printf("  skip  %-30s %s\n", c.Name, "Holds are released on demand (--worker=false).")
	}
	fmt.Printf("\n%d checks, %d failed, %d skipped.\n", len(r.Findings), failed, len(r.Skipped))
}
