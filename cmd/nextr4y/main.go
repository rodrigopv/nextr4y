package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/fatih/color"                       // Import color package
	"github.com/rodrigopv/nextr4y/internal/fetch"
	"github.com/rodrigopv/nextr4y/internal/mcpserver"
	"github.com/rodrigopv/nextr4y/internal/scanner"
	"github.com/rodrigopv/nextr4y/internal/versiondetect"
	"github.com/urfave/cli/v2"
	// TODO: Import github.com/mark3labs/mcp-go when it's available for implementation
)

// Build information, initialized to defaults and potentially overridden by ldflags.
var (
	version = "development" // Git tag or version number
	commit  = "n/a"         // Git commit hash
	date    = "n/a"         // Build date
)

func printBanner() {
	lineColor := color.New(color.FgYellow)
	nameColor := color.New(color.FgWhite, color.Bold)
	urlColor := color.New(color.FgCyan)
	metaColor := color.New(color.FgWhite) // Color for version/commit/date
	width := 64 // Width of the content area inside the box
	border := "+" + strings.Repeat("-", width) + "+"
	nameText := "nextr4y"
	urlText := "github.com/rodrigopv/nextr4y" // Corrected repo name

	// Calculate total padding needed
	namePaddingTotal := width - len(nameText)
	urlPaddingTotal := width - len(urlText)

	// Split padding (integer division handles odd/even)
	namePaddingLeft := strings.Repeat(" ", namePaddingTotal/2)
	namePaddingRight := strings.Repeat(" ", width-len(nameText)-(namePaddingTotal/2)) // Calculate remainder

	urlPaddingLeft := strings.Repeat(" ", urlPaddingTotal/2)
	urlPaddingRight := strings.Repeat(" ", width-len(urlText)-(urlPaddingTotal/2)) // Calculate remainder

	lineColor.Println(border)
	lineColor.Print("|")      // Print starting pipe (colored)
	fmt.Print(namePaddingLeft) // Print left padding (no color)
	nameColor.Print(nameText)  // Print colored name
	fmt.Print(namePaddingRight)// Print right padding (no color)
	lineColor.Println("|")     // Print ending pipe and newline (colored)

	lineColor.Print("|")     // Print starting pipe (colored)
	fmt.Print(urlPaddingLeft) // Print left padding (no color)
	urlColor.Print(urlText)   // Print colored url
	fmt.Print(urlPaddingRight)// Print right padding (no color)
	lineColor.Println("|")    // Print ending pipe and newline (colored)

	lineColor.Println(border)

	// Print Build Info
	buildInfo := fmt.Sprintf("Version: %s | Commit: %s | Date: %s", version, commit, date)
	fmt.Printf("%s\n\n", metaColor.Sprint(buildInfo))
}

// scanAction is the default scan action
func scanAction(c *cli.Context) error {
	if c.NArg() != 1 {
		cli.ShowCommandHelpAndExit(c, c.Command.Name, 1) // Show help if URL is missing
	}
	targetURL := c.Args().Get(0)
	outputFile := c.String("output")
	outputFormat := c.String("format")
	customBaseURL := c.String("base-url")

	if outputFormat != "text" && outputFormat != "json" {
		return cli.Exit(fmt.Sprintf("Error: Invalid output format '%s'. Use 'text' or 'json'.", outputFormat), 1)
	}

	log.Printf("Scanning target: %s", targetURL)
	if customBaseURL != "" {
		log.Printf("Using custom base URL: %s", customBaseURL)
	}

	// Create the fetcher and scanner instances
	fetcher := fetch.NewHTTPFetcher()
	versionDetector := &versiondetect.HeuristicAssetScannerDetector{}
	scr := scanner.NewScanner(fetcher, versionDetector, customBaseURL) // Pass the custom base URL

	// Call the ScanTarget method
	result, err := scr.ScanTarget(targetURL)
	if err != nil {
		// Log the error, but proceed to print/write partial results if available
		log.Printf("Scan encountered an error: %v", err)
		// Assign error to result if not already set (e.g., for invalid URL)
		if result != nil && result.ExecutionError == nil {
			result.ExecutionError = err
		} else if result == nil {
			// Handle cases where ScanTarget returns nil result (e.g., invalid final URL parse)
			return cli.Exit(fmt.Sprintf("Critical error during scan setup: %v", err), 1)
		}
	}

	// Handle output
	if outputFile != "" {
		err := scanner.WriteOutput(result, outputFile, outputFormat)
		if err != nil {
			return cli.Exit(fmt.Sprintf("Error writing output file: %v", err), 1)
		}
	} else {
		err := scanner.PrintResults(result, outputFormat)
		if err != nil {
			// This should ideally not happen if format validation is done
			return cli.Exit(fmt.Sprintf("Error printing results: %v", err), 1)
		}
	}

	// Indicate if there was a non-critical error during the scan
	if result != nil && result.ExecutionError != nil {
		// Return a non-zero exit code to indicate partial failure
		// Return nil here to let the log message suffice, or return the error string?
		// Let's return nil for now, the log indicates the issue. User can use JSON output for details.
		log.Printf("Scan completed with errors (see logs or JSON output for details).")
	} else {
		log.Println("Scan completed successfully.")
	}

	return nil
}

// serveAction is the action for the serve command
func serveAction(c *cli.Context) error {
	port := c.Int("port")
	host := c.String("host")
	
	log.Printf("Starting MCP server on %s:%d", host, port)
	log.Printf("The server accepts nextr4y scan requests via MCP protocol")
	
	// Create and start the MCP server
	server := mcpserver.NewMCPServer(host, port)
	return server.Start()
}

func main() {
	printBanner() // Print the banner first

	// Common flags for scan command
	scanFlags := []cli.Flag{
		&cli.StringFlag{
			Name:    "output",
			Aliases: []string{"o"},
			Value:   "", // Default is stdout
			Usage:   "Write output to `FILE`",
		},
		&cli.StringFlag{
			Name:    "format",
			Aliases: []string{"f"},
			Value:   "text", // Default format
			Usage:   "Output format (`text` or `json`)",
		},
		&cli.StringFlag{
			Name:    "base-url",
			Aliases: []string{"b"},
			Value:   "", // Default is empty (use auto-detection)
			Usage:   "Override the auto-detected base URL for asset resolution",
		},
	}

	// Serve command flags
	serveFlags := []cli.Flag{
		&cli.IntFlag{
			Name:    "port",
			Aliases: []string{"p"},
			Value:   8080,
			Usage:   "Port for the MCP server",
		},
		&cli.StringFlag{
			Name:    "host",
			Value:   "0.0.0.0",
			Usage:   "Host for the MCP server",
		},
	}

	app := &cli.App{
		Name:      "nextr4y",
		Usage:     "Uncover the hidden internals of Next.js sites.",
		UsageText: "nextr4y [command] [command options] [arguments...]",
		Commands: []*cli.Command{
			{
				Name:      "scan",
				Usage:     "Scan a Next.js site",
				UsageText: "nextr4y scan [options] <target_url>",
				Flags:     scanFlags,
				Action:    scanAction,
			},
			{
				Name:      "serve",
				Usage:     "Start an MCP server to handle nextr4y scan requests",
				UsageText: "nextr4y serve [options]",
				Flags:     serveFlags,
				Action:    serveAction,
			},
		},
		// Show help when no command is specified instead of defaulting to scan
		Action: func(c *cli.Context) error {
			cli.ShowAppHelp(c)
			return cli.Exit("No command specified. Please provide a command (scan or serve).", 1)
		},
		// We still need flags in case -h or --help is used
		Flags: []cli.Flag{},
	}

	// Customize Help Printer
	cli.AppHelpTemplate = fmt.Sprintf(`%s
%s`, cli.AppHelpTemplate, `EXAMPLES:
   nextr4y scan https://example.com
   nextr4y scan -f json -o results.json https://vercel.com
   nextr4y scan -b https://cdn.example.com https://example.com
   nextr4y serve -p 8080
`)

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err) // Log fatal errors from cli itself
	}
} 