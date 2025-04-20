package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/rodrigopv/nextr4y/internal/fetch"
	"github.com/rodrigopv/nextr4y/internal/scanner"
	"github.com/rodrigopv/nextr4y/internal/versiondetect"
)

// MCPServer represents an MCP server instance
type MCPServer struct {
	host      string
	port      int
	mcpServer *server.MCPServer
}

// NewMCPServer creates a new MCP server instance
func NewMCPServer(host string, port int) *MCPServer {
	return &MCPServer{
		host: host,
		port: port,
	}
}

// Start starts the MCP server
func (s *MCPServer) Start() error {
	addr := fmt.Sprintf("%s:%d", s.host, s.port)
	log.Printf("Starting MCP server on %s\n", addr)
	
	// Initialize MCP server
	err := s.InitMCPServer()
	if err != nil {
		return fmt.Errorf("failed to initialize MCP server: %w", err)
	}
	
	// Check if we have the MCP server implementation
	if s.mcpServer == nil {
		return fmt.Errorf("MCP server implementation not available")
	}
	
	// Use the MCP server
	log.Printf("Starting MCP server with mark3labs/mcp-go implementation")
	return s.StartMCPServer()
}

// MCPTool represents a tool that can be registered with the MCP server
type MCPTool struct {
	Name        string
	Description string
	Handler     func(params map[string]interface{}) (interface{}, error)
}

// RegisterScanTool registers the scan tool with the MCP server
func (s *MCPServer) RegisterScanTool() *MCPTool {
	return &MCPTool{
		Name:        "nextr4y_scan",
		Description: "Scan a Next.js site and extract information about its internal structure",
		Handler:     s.handleScanRequest,
	}
}

// handleScanRequest handles a scan request from an MCP client
func (s *MCPServer) handleScanRequest(params map[string]interface{}) (interface{}, error) {
	// Extract target URL from parameters
	targetURL, ok := params["url"].(string)
	if !ok || targetURL == "" {
		return nil, fmt.Errorf("missing or invalid target URL")
	}

	// Extract options
	options := make(map[string]interface{})
	if format, ok := params["format"].(string); ok {
		options["format"] = format
	}
	if baseURL, ok := params["base_url"].(string); ok {
		options["base_url"] = baseURL
	}

	log.Printf("Received scan request for target: %s", targetURL)

	// Create scanner and perform scan
	fetcher := fetch.NewHTTPFetcher()
	versionDetector := &versiondetect.HeuristicAssetScannerDetector{}
	customBaseURL, _ := options["base_url"].(string)
	scr := scanner.NewScanner(fetcher, versionDetector, customBaseURL)

	// Execute the scan
	result, err := scr.ScanTarget(targetURL)
	if err != nil {
		log.Printf("Scan error: %v", err)
		// Still return partial results if available
		if result != nil {
			result.ExecutionError = err
			return result, nil
		}
		return nil, err
	}

	return result, nil
}

// InitMCPServer initializes the MCP server with mcp-go
func (s *MCPServer) InitMCPServer() error {
	log.Println("Initializing MCP server...")
	
	// Create a new MCP server
	mcpServer := server.NewMCPServer(
		"nextr4y",
		"1.0.0",
		server.WithLogging(),
		server.WithRecovery(),
	)
	
	// Create the scan tool
	scanTool := mcp.NewTool("nextr4y_scan",
		mcp.WithDescription("Scan a Next.js site and extract information about its internal structure"),
		mcp.WithString("url",
			mcp.Required(),
			mcp.Description("The URL of the target Next.js site to scan"),
		),
		mcp.WithString("format",
			mcp.Description("Output format (text or json)"),
			mcp.Enum("text", "json"),
		),
		mcp.WithString("base_url",
			mcp.Description("Override the auto-detected base URL for asset resolution"),
		),
	)
	
	// Register the scan tool handler
	mcpServer.AddTool(scanTool, s.handleScanToolRequest)
	
	// Set the MCP server in the MCPServer struct
	s.mcpServer = mcpServer
	
	log.Println("MCP server initialized successfully")
	return nil
}

// StartMCPServer starts the MCP server 
func (s *MCPServer) StartMCPServer() error {
	if s.mcpServer == nil {
		return fmt.Errorf("MCP server not initialized")
	}
	
	addr := fmt.Sprintf("%s:%d", s.host, s.port)
	log.Printf("Starting MCP server on %s\n", addr)
	
	// Create an SSE server for HTTP communication
	sseServer := server.NewSSEServer(s.mcpServer)
	
	// Start the HTTP server
	return sseServer.Start(addr)
}

// handleScanToolRequest handles scan tool requests from MCP clients
func (s *MCPServer) handleScanToolRequest(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Extract parameters
	targetURL, ok := request.Params.Arguments["url"].(string)
	if !ok || targetURL == "" {
		return mcp.NewToolResultError("Missing or invalid target URL"), nil
	}
	
	// Extract optional parameters
	format := "json"
	if fmt, ok := request.Params.Arguments["format"].(string); ok && fmt != "" {
		format = fmt
	}
	
	baseURL := ""
	if url, ok := request.Params.Arguments["base_url"].(string); ok {
		baseURL = url
	}
	
	log.Printf("Received scan request for target: %s (format: %s)", targetURL, format)
	
	// Create scanner and perform scan
	fetcher := fetch.NewHTTPFetcher()
	versionDetector := &versiondetect.HeuristicAssetScannerDetector{}
	scr := scanner.NewScanner(fetcher, versionDetector, baseURL)
	
	// Execute the scan
	result, err := scr.ScanTarget(targetURL)
	if err != nil {
		log.Printf("Scan error: %v", err)
		// Still return partial results if available
		if result != nil {
			result.ExecutionError = err
			
			// Convert the result to JSON for returning
			jsonData, jsonErr := json.MarshalIndent(result, "", "  ")
			if jsonErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("Error scanning target: %v, and error converting results: %v", err, jsonErr)), nil
			}
			
			// Return partial results with error message
			return mcp.NewToolResultText(
				fmt.Sprintf("Scan completed with errors:\n%v\n\nPartial results:\n%s", err, string(jsonData)),
			), nil
		}
		
		return mcp.NewToolResultError(fmt.Sprintf("Error scanning target: %v", err)), nil
	}
	
	// Process the result based on the requested format
	if format == "json" {
		// Convert to JSON
		jsonData, jsonErr := json.MarshalIndent(result, "", "  ")
		if jsonErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error converting results to JSON: %v", jsonErr)), nil
		}
		
		return mcp.NewToolResultText(string(jsonData)), nil
	} else {
		// Format as text (would normally call scanner.PrintResults to get the text representation)
		// For demonstration, we'll create a simple text version here
		var text string
		text += fmt.Sprintf("Target: %s\n", result.BaseURL)
		text += fmt.Sprintf("Is Next.js: %v\n", result.IsNextJS)
		if result.IsNextJS {
			text += fmt.Sprintf("Build ID: %s\n", result.BuildID)
			text += fmt.Sprintf("Next.js Version: %s\n", result.DetectedNextVersion)
			text += fmt.Sprintf("React Version: %s\n", result.DetectedReactVersion)
			text += fmt.Sprintf("Asset Prefix: %s\n", result.AssetPrefix)
			text += fmt.Sprintf("Asset Base URL: %s\n", result.AssetBaseURL)
			text += fmt.Sprintf("Routes found: %d\n", len(result.Routes))
			
			// Add routes
			for route, assets := range result.Routes {
				text += fmt.Sprintf("  - %s (%d assets)\n", route, len(assets))
			}
		}
		
		return mcp.NewToolResultText(text), nil
	}
} 