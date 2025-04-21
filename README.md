# nextr4y

<p align="center">
  <img src="docs/logo.jpg" alt="nextr4y Logo" />
  <br>
  <b>Uncover the hidden internals of Next.js sites</b>
  <br>
</p>

<p align="center">
  <a href="#features">Features</a> •
  <a href="#installation">Installation</a> •
  <a href="#using-with-devbox">Using with Devbox</a> •
  <a href="#usage">Usage</a> •
  <a href="#examples">Examples</a> •
  <a href="#contributing">Contributing</a> •
  <a href="#license">License</a>
</p>

---

**nextr4y** is a powerful reconnaissance tool written in Golang designed to analyze Next.js applications and extract valuable information about their internal structure, routes, and dependencies. By scanning a target Next.js site, nextr4y can reveal build IDs, Next.js and React versions, asset prefixes, and route mappings that can be valuable for security assessments, debugging, or reverse engineering. It also features an MCP server mode for remote scanning and integration. Built with performance and reliability in mind, this Go-based tool is perfect for cybersecurity professionals and web application researchers.

## Features

- 🔍 **Next.js Detection** - Automatically detect if a site is built with Next.js
- 🏗️ **Version Fingerprinting** - Identify both Next.js and React versions in use
- 🗺️ **Route Mapping** - Discover and map internal routes defined in the application
- 📦 **Asset Discovery** - Identify and catalog JavaScript and CSS assets
- 🔧 **Build Manifest Analysis** - Extract and analyze the build manifest
- 📊 **Multiple Output Formats** - Get results in human-readable text or machine-parsable JSON
- 🔒 **Anti-Bot Evasion** - Uses CycleTLS-based page fetcher with different JA3 fingerprints and user agent presets to avoid bot detection
- 🌐 **MCP Server Mode** - Expose scanning functionality via a Model Context Protocol server for remote access and integration (e.g., with Cursor)


## Sample scan
<img src="docs/samplescan.png" alt="nextr4y scan example" />

## Installation

### From Source

```bash
# Clone the repository
git clone https://github.com/rodrigopv/nextr4y.git
cd nextr4y

# Build the binary
go build -o nextr4y ./cmd/nextr4y
```

### Using Go Install

```bash
go install github.com/rodrigopv/nextr4y/cmd/nextr4y@latest
```

### Pre-built Binaries

Download pre-built binaries from the [Releases](https://github.com/rodrigopv/nextr4y/releases) page.

## Using with Devbox

[Devbox](https://www.jetify.com/devbox/) provides a consistent development environment with all dependencies pre-configured. This ensures that everyone using the project has the same tooling and versions, eliminating "works on my machine" issues.

### Installing Devbox

```bash
# Follow the official installation instructions at:
# https://www.jetify.com/devbox/docs/installing_devbox/
```

### Using nextr4y with Devbox

After installing Devbox, you can run nextr4y without installing Go or any other dependencies:

```bash
# Clone and enter the repository
git clone https://github.com/rodrigopv/nextr4y
cd nextr4y

# Build the binary
devbox run build

# Run a scan
devbox run scan https://example-nextjs-site.com

# Run a scan with JSON output
devbox run scan:json https://example-nextjs-site.com

# Start the MCP server
devbox run serve
```

### Available Devbox Commands

- `devbox run build` - Build the nextr4y binary
- `devbox run test` - Run all tests
- `devbox run lint` - Run linter
- `devbox run scan [url]` - Scan a Next.js site
- `devbox run serve` - Start the MCP server

## Usage

```
nextr4y [command] [command options] [arguments...]
```

### Commands

```
COMMANDS:
   scan    Scan a Next.js site
   serve   Start an MCP server to handle nextr4y scan requests
   help    Shows a list of commands or help for one command
```

### Scan Command Options

```
OPTIONS:
   --output FILE, -o FILE  Write output to FILE
   --format value, -f value  Output format (text or json) (default: "text")
   --base-url value, -b value  Override the auto-detected base URL for asset resolution
   --help, -h              Show help information
```

### Serve Command Options

```
OPTIONS:
   --port value, -p value  Port for the MCP server (default: 8080)
   --host value           Host for the MCP server (default: "0.0.0.0")
   --help, -h             Show help information
```

## Examples

### Basic Scan

```bash
nextr4y https://example-nextjs-site.com
```

### Detailed Output to JSON File

```bash
nextr4y -f json -o results.json https://vercel.com
```

### Custom Base URL

```bash
nextr4y -b https://cdn.example.com https://example.com
```

### Starting the MCP Server

```bash
nextr4y serve -p 9000 -host 127.0.0.1
```

### Sample Output (Text Format)

```
+ ---------------------------------------------------------------- +
|                            nextr4y                               |
|                  github.com/rodrigopv/nextr4y                    |
+ ---------------------------------------------------------------- +

Scanning target: https://example-nextjs-site.com

Target is using Next.js: ✅
Build ID: 1a2b3c4d5e6f7g8h9i0j
Detected Next.js Version: 13.4.12
Detected React Version: 18.2.0
Asset Prefix: 
Calculated Asset Base URL: https://example-nextjs-site.tld/
Build Manifest Found: ✅
Build Manifest Executed OK: ✅
Routes (12 routes found):
  - / (18 assets)
  - /about (15 assets)
  - /blog (22 assets)
  - /blog/[slug] (24 assets)
  - /admin/reception/id-card (10 assets)
- /admin/reception/passport (10 assets)
  ...
Found 123 unique assets from manifest.
```

### Sample Output (JSON Format)

```json
{
  "BaseURL": "https://example.com/",
  "AssetBaseURL": "https://example.com/_next/",
  "IsNextJS": true,
  "BuildID": "SAMPLE_BUILD_ID_123",
  "AssetPrefix": "/_next",
  "Routes": {
    "/about": [
      "https://example.com/_next/static/chunks/pages/about-a1b2c3d4e5f6a7b8.js",
      "https://example.com/_next/static/chunks/framework-12345abcde.js",
      "https://example.com/_next/static/css/styles-about-abcdef.css"
    ],
    "/products/[productId]": [
      "https://example.com/_next/static/chunks/pages/products/%5BproductId%5D-f1e2d3c4b5a6f7e8.js",
      "https://example.com/_next/static/chunks/framework-12345abcde.js",
      "https://example.com/_next/static/chunks/shared-component-lib-xyz789.js",
      "https://example.com/_next/static/css/styles-products-fedcba.css"
    ]
  },
  "AllAssets": {
    "https://example.com/_next/static/chunks/pages/about-a1b2c3d4e5f6a7b8.js": true,
    "https://example.com/_next/static/chunks/framework-12345abcde.js": true,
    "https://example.com/_next/static/css/styles-about-abcdef.css": true,
    "https://example.com/_next/static/chunks/pages/products/%5BproductId%5D-f1e2d3c4b5a6f7e8.js": true,
    "https://example.com/_next/static/chunks/shared-component-lib-xyz789.js": true,
    "https://example.com/_next/static/css/styles-products-fedcba.css": true
  },
  "ManifestFound": true,
  "ManifestExecOK": true,
  "ExecutionError": null,
  "NextDataJSONRaw": "{\"props\":{\"pageProps\":{\"sampleData\": true, \"message\": \"This is placeholder _next/data content.\"}}}",
  "DetectedNextVersion": "14.1.0",
  "DetectedReactVersion": "18.2.0"
}
```

## How It Works

nextr4y works by:

1. **Initial Scanning** - Fetches the target page and looks for Next.js-specific markers
2. **__NEXT_DATA__ Extraction** - Parses the embedded Next.js configuration data
3. **Asset Detection** - Identifies JavaScript and CSS assets linked in the HTML
4. **Build Manifest Analysis** - Downloads and analyzes the build manifest to map routes
5. **Version Detection** - Uses multiple strategies to fingerprint Next.js and React versions
6. **Report Generation** - Compiles discovered data into structured output
7. **Bot Detection Evasion** - Implements CycleTLS for TLS fingerprint randomization with various JA3 signatures and rotating user agents to bypass common bot detection systems
8. **MCP Server Mode** - Provides a Model Context Protocol server interface to execute scans remotely

## MCP Server

The MCP (Message Context Protocol) server mode allows nextr4y to be used as a service that accepts scan requests remotely. This is useful for:

- **Integration** - Incorporate nextr4y scanning into your own applications or workflows
- **Automation** - Schedule and automate scans of Next.js sites
- **API Access** - Access nextr4y functionality through a standardized API interface
- **AI Integration Bridge** - Serve as a bridge between the data provided by nextr4y and AI-driven tools or solutions (like Cursor) for enhanced analysis and interaction.

When using the MCP server, clients can send requests to scan specific targets and receive the scan results as structured responses. The server handles the execution of the scans and returns the results to the client.

### Using the MCP Server

Start the MCP server:

```bash
nextr4y serve -p 8080 -host 0.0.0.0
```

#### Available Tools

The MCP server provides the following tools:

- **nextr4y_scan** - Scan a Next.js site and extract information about its structure
  - Parameters:
    - `url` (string, required) - The URL of the target Next.js site
    - `format` (string, optional) - Output format ("json" or "text", defaults to "json")
    - `base_url` (string, optional) - Custom base URL for asset resolution

### Using with Cursor

You can integrate nextr4y with Cursor IDE using the MCP protocol:

1. Start the nextr4y MCP server:

```bash
go run github.com/rodrigopv/nextr4y/cmd/nextr4y serve
```

2. Create or edit the Cursor MCP configuration file at `~/.cursor/mcp.json`:

```json
{
  "mcpServers": {
    "nextr4y": {
      "url": "http://localhost:8080/sse"
    }
  }
}
```

3. Restart Cursor for the changes to take effect.

4. You can now use nextr4y from within Cursor to scan Next.js sites and analyze their structure.

## Use Cases

- **Security Research** - Reconnaissance and analysis of Next.js application structure
- **Penetration Testing** - Map routes and identify potential API endpoints
- **Website Analysis** - Learn how sites are built and structured with Next.js
- **Internal View Reconstruction** - Use MCP to connect nextr4y data (routes, assets) to IDEs such as cursor to understand or mimic internal application views for deeper analysis or vulnerability hunting.

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add some amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

See [CONTRIBUTING.md](CONTRIBUTING.md) for more information.

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## Disclaimer

nextr4y is designed for legitimate security research and web development purposes only. Use responsibly and only against websites you own or have explicit permission to test. The authors are not responsible for any misuse of this tool.

---
