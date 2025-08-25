# TimeSurferProxy

A GO-based HTTP proxy that allows clients to browse the internet as it appeared on a specific date in the past, using the Wayback Machine archives.  Simply enter in a URL in to your web browser, and let the proxy take you back in time!  

It also allows browsing of archived GeoCities sites through restorativland.

## Features

- Proxies HTTP requests to the Wayback Machine for a specific date
- Removes Wayback Machine toolbar for authentic retro browsing
- Supports direct access to geocities.restorativland.org for browsing archived Geocities content
- Supports debug mode for troubleshooting

## Installation

### Option 1: Download Pre-compiled Binary (Recommended)
1. Download the latest release for your operating system from the [Releases](https://github.com/yourusername/timesurferproxy/releases) page
2. Extract the archive if needed
3. The executable is ready to use with the switches below

### Option 2: Build from Source
1. Install Go (https://golang.org/dl/)
2. Clone or download this repository
3. Build the executable

## Usage

### Running the Proxy
```
timesurfer.exe -port PORT -date YYYYMMDD [-debug]
```

### Parameters

- `-port`: Port number for the proxy to listen on (default: 8080)
- `-date`: Date in YYYYMMDD format to browse the internet as it appeared on that date
- `-debug`: Enable debug logging (optional)

### Example

```
timesurfer.exe -port 1080 -date 20020401
```

This will start the proxy on port 1080 for April 4, 2002.

## Configuration

### Sample Browser Configuration

1. Open Internet Explorer 5
2. Go to Tools → Internet Options → Connections → LAN Settings
3. Check "Use a proxy server"
4. Enter the proxy server address (e.g., 192.168.1.100) and port (e.g., 1080)
5. Click OK to save

### Browsing Archived Geocities Content

Users can directly visit `http://geocities.restorativland.org` to browse archived Geocities content. To improve performance on retro computers, the proxy removes screenshot images from directory listings while preserving navigation functionality.

Users can navigate through the archived Geocities content by clicking links to subdirectories and pages, with all traffic being proxied through this application.

## How Wayback Access Works

1. When a request is made to a website, the proxy queries the Wayback Machine's API to find an archived version from the specified date
2. The proxy then redirects the request to the archived version
3. HTML responses are modified to remove the Wayback Machine toolbar
4. Embedded objects like images and resources are automatically proxied through the same date-specific archive
5. Intelligent redirect handling ensures seamless navigation while maintaining proxy integrity           

## Limitations

- Some websites may not have been archived by the Wayback Machine
- Complex web applications from the early 2000s may not function exactly as they did originally