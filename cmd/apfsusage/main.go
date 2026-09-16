package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/benvon/macos-system-files-cleaner/internal/scan"
	"golang.org/x/sys/unix"
)

var version = "dev"

const usage = `apfsusage is a read-only macOS storage discovery prototype.

Usage:
  apfsusage probe
  apfsusage scan [flags] PATH [PATH...]

Commands:
  probe  Show APFS capacity, Spotlight status, and local snapshot names.
  scan   Measure allocated and apparent bytes with macOS bulk metadata reads.
  version Show the build version.

Run "apfsusage scan -h" for scan flags.
`

type scanOutput struct {
	GeneratedAt string        `json:"generated_at"`
	Results     []scan.Result `json:"results"`
}

func main() {
	if runtime.GOOS != "darwin" {
		fatalf("apfsusage supports macOS only")
	}
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	switch os.Args[1] {
	case "probe":
		if err := probe(); err != nil {
			fatalf("probe: %v", err)
		}
	case "scan":
		if err := runScan(os.Args[2:]); err != nil {
			fatalf("scan: %v", err)
		}
	case "version":
		fmt.Println(version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}

func probe() error {
	fmt.Println("Read-only storage probe")
	fmt.Printf("macOS: %s (%s)\n", commandText("/usr/bin/sw_vers", "-productVersion"), runtime.GOARCH)

	for _, path := range []string{"/", "/System/Volumes/Data"} {
		var stat unix.Statfs_t
		if err := unix.Statfs(path, &stat); err != nil {
			fmt.Printf("\n%s: unavailable: %v\n", path, err)
			continue
		}
		blockSize := uint64(stat.Bsize)
		total := uint64(stat.Blocks) * blockSize
		free := uint64(stat.Bfree) * blockSize
		available := uint64(stat.Bavail) * blockSize
		used := total - free
		fmt.Printf("\n%s (%s)\n", path, cString(stat.Fstypename[:]))
		fmt.Printf("  total:        %12s\n", humanBytes(total))
		fmt.Printf("  allocated:    %12s\n", humanBytes(used))
		fmt.Printf("  free:         %12s\n", humanBytes(free))
		fmt.Printf("  user avail:   %12s\n", humanBytes(available))
	}

	fmt.Println("\nSpotlight status")
	fmt.Println(indent(commandText("/usr/bin/mdutil", "-s", "/"), "  "))
	fmt.Println("\nLocal Time Machine snapshots")
	fmt.Println(indent(commandText("/usr/bin/tmutil", "listlocalsnapshots", "/"), "  "))
	fmt.Println("\nNotes")
	fmt.Println("  - APFS volumes in one container share free space; volume figures are not additive.")
	fmt.Println("  - macOS may count purgeable snapshot space as available.")
	fmt.Println("  - Spotlight can accelerate hints, but it is not an authoritative inventory.")
	return nil
}

func runScan(args []string) error {
	flags := flag.NewFlagSet("scan", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	top := flags.Int("top", 20, "number of largest immediate children to report")
	workers := flags.Int("workers", min(runtime.NumCPU(), 8), "maximum concurrent directory scans")
	oneFileSystem := flags.Bool("one-file-system", true, "do not descend into other mounted filesystems")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() == 0 {
		return errors.New("at least one PATH is required")
	}
	if *top < 0 {
		return errors.New("-top must be zero or greater")
	}
	if *workers < 1 || *workers > 128 {
		return errors.New("-workers must be between 1 and 128")
	}

	results := make([]scan.Result, 0, flags.NArg())
	for _, path := range flags.Args() {
		if !*jsonOutput {
			fmt.Fprintf(os.Stderr, "Scanning %s...\n", quotePath(path))
		}
		result, err := scan.Path(path, scan.Options{
			Top:           *top,
			Workers:       *workers,
			OneFileSystem: *oneFileSystem,
		})
		if err != nil {
			return fmt.Errorf("scan %s: %w", quotePath(path), err)
		}
		results = append(results, result)
	}

	if *jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(scanOutput{
			GeneratedAt: time.Now().UTC().Format(time.RFC3339),
			Results:     results,
		})
	}

	for i, result := range results {
		if i > 0 {
			fmt.Println()
		}
		printResult(result)
	}
	return nil
}

func printResult(result scan.Result) {
	fmt.Printf("%s\n", quotePath(result.Path))
	fmt.Printf("  allocated:       %12s\n", humanBytes(result.AllocatedBytes))
	fmt.Printf("  apparent:        %12s\n", humanBytes(result.ApparentBytes))
	if result.ReclaimableFiles > 0 {
		fmt.Printf("  immediate free:  %12s  (%d/%d files measured)\n",
			humanBytes(result.ImmediateReclaimableBytes), result.ReclaimableFiles, result.Files)
	}
	fmt.Printf("  files:           %12d\n", result.Files)
	fmt.Printf("  directories:     %12d\n", result.Directories)
	if result.CloneFiles > 0 || result.SharedBlockFiles > 0 || result.SparseFiles > 0 || result.PurgeableItems > 0 || result.SyncRoots > 0 {
		fmt.Printf("  APFS features:   %12d clones, %d may-share, %d sparse, %d purgeable, %d sync roots\n",
			result.CloneFiles, result.SharedBlockFiles, result.SparseFiles, result.PurgeableItems, result.SyncRoots)
	}
	fmt.Printf("  hard links seen: %12d\n", result.DuplicateHardLinks)
	fmt.Printf("  skipped mounts:  %12d\n", result.SkippedMounts)
	fmt.Printf("  unreadable dirs: %12d\n", result.UnreadableDirectories)
	fmt.Printf("  elapsed:         %12s\n", result.Elapsed.Round(time.Millisecond))
	if result.Elapsed > 0 {
		fmt.Printf("  entries/second:  %12.0f\n", float64(result.Files+result.Directories)/result.Elapsed.Seconds())
	}
	if result.FallbackDirectories > 0 {
		fmt.Printf("  fallback dirs:   %12d (bulk metadata unavailable; used readdir/stat)\n", result.FallbackDirectories)
	}
	if result.Volume != nil {
		fmt.Println("\n  Volume reconciliation")
		fmt.Printf("  container allocated: %12s\n", humanBytes(result.Volume.AllocatedBytes))
		fmt.Printf("  observed live tree:  %12s\n", humanBytes(result.AllocatedBytes))
		fmt.Printf("  unobserved residual: %12s\n", humanBytes(result.Volume.ResidualBytes))
		fmt.Println("  Residual includes other shared APFS volumes, snapshots, filesystem metadata,")
		fmt.Println("  inaccessible paths, and measurement effects; it is not all reclaimable.")
	}

	if len(result.Top) > 0 {
		fmt.Println("\n  Largest immediate children (allocated bytes)")
		for _, item := range result.Top {
			fmt.Printf("  %12s  %s", humanBytes(item.AllocatedBytes), quotePath(item.Path))
			var labels []string
			if item.Role != "" {
				labels = append(labels, item.Role)
			}
			if item.PackageKind != "" {
				labels = append(labels, item.PackageKind)
			}
			labels = append(labels, item.Properties...)
			if len(labels) > 0 {
				fmt.Printf("  [%s]", strings.Join(labels, ", "))
			}
			fmt.Println()
			if item.Producer != nil {
				fmt.Printf("                producer: %s (%s confidence)\n",
					quotePath(item.Producer.Identifier), item.Producer.Confidence)
			}
			if item.ReclaimableFiles > 0 {
				fmt.Printf("                immediate free: %s (%d/%d files measured)\n",
					humanBytes(item.ImmediateReclaimableBytes), item.ReclaimableFiles, item.Files)
			}
		}
	}
	if len(result.Errors) > 0 {
		fmt.Println("\n  Sample access/errors")
		for _, item := range result.Errors {
			fmt.Printf("  - %s: %s\n", quotePath(item.Path), item.Error)
		}
	}

	fmt.Println("\n  Interpretation: allocated sizes are filesystem evidence, not Apple's private")
	fmt.Println("  System Data classification. APFS clones can make per-file allocated totals")
	fmt.Println("  overstate unique physical blocks. Immediate-free bytes use APFS private size")
	fmt.Println("  where available; deleting several related clones may free additional shared")
	fmt.Println("  blocks, while snapshots can retain blocks after deletion.")
}

func commandText(name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = nil
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "unavailable: command timed out"
		}
		if text == "" {
			return "unavailable: " + err.Error()
		}
		return text + " (command failed: " + err.Error() + ")"
	}
	if text == "" {
		return "none"
	}
	return text
}

func quotePath(path string) string {
	return strconv.QuoteToGraphic(path)
}

func terminalSafe(value string) string {
	quoted := strconv.QuoteToGraphic(value)
	return quoted[1 : len(quoted)-1]
}

func cString(value []byte) string {
	bytes := make([]byte, 0, len(value))
	for _, b := range value {
		if b == 0 {
			break
		}
		bytes = append(bytes, b)
	}
	return string(bytes)
}

func indent(value, prefix string) string {
	return prefix + strings.ReplaceAll(value, "\n", "\n"+prefix)
}

func humanBytes(value uint64) string {
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	divisor, exponent := uint64(unit), 0
	for quotient := value / unit; quotient >= unit && exponent < 5; quotient /= unit {
		divisor *= unit
		exponent++
	}
	return fmt.Sprintf("%.1f %ciB", float64(value)/float64(divisor), "KMGTPE"[exponent])
}

func fatalf(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr, "apfsusage: %s\n", terminalSafe(message))
	os.Exit(1)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
