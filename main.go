package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	port     = flag.String("port", "8080", "Port to listen on")
	date     = flag.String("date", "", "Date in YYYYMMDD format")
	debug    = flag.Bool("debug", false, "Enable debug logging")
	maxRetries = flag.Int("max-retries", 3, "Maximum number of retries for failed requests")
	retryDelay = flag.Duration("retry-delay", 1*time.Second, "Initial delay between retries")
)

func debugLog(format string, v ...interface{}) {
	if *debug {
		log.Printf("[DEBUG] "+format, v...)
	}
}

func errorLog(format string, v ...interface{}) {
	log.Printf("[ERROR] "+format, v...)
}

func removeWaybackToolbar(html string) string {
	// Remove the Wayback toolbar
	start := strings.Index(html, "<!-- BEGIN WAYBACK TOOLBAR INSERT -->")
	end := strings.Index(html, "<!-- END WAYBACK TOOLBAR INSERT -->")
	
	if start != -1 && end != -1 {
		html = html[:start] + html[end+36:] // 36 is length of end comment
	}
	
	// Remove the tracking javascript
	scriptTag := `<script src="//archive.org/includes/athena.js" type="text/javascript"></script>`
	html = strings.Replace(html, scriptTag, "", -1)
	
	return html
}

func getWaybackURL(originalURL string, date string) (string, error) {
	// Call the CDX API to get the archived URL
	cdxURL := fmt.Sprintf("http://web.archive.org/cdx/search/cdx?url=%s&from=%s&filter=statuscode:200&filter=mimetype:text/html&limit=1&output=json", 
		url.QueryEscape(originalURL), date)
	
	debugLog("Calling CDX API: %s", cdxURL)
	
	// Use a dedicated client for CDX API calls with default transport
	client := &http.Client{
		Timeout: 90 * time.Second,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
		},
	}
	resp, err := client.Get(cdxURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	
	// Check status code
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("CDX API returned status %d", resp.StatusCode)
	}
	
	var cdxResp []interface{}
	if err := json.NewDecoder(resp.Body).Decode(&cdxResp); err != nil {
		return "", err
	}
	
	// Check if we have results
	if len(cdxResp) < 2 {
		return "", fmt.Errorf("no archived version found for %s", originalURL)
	}
	
	// Extract timestamp from the second row (first row is headers)
	row, ok := cdxResp[1].([]interface{})
	if !ok || len(row) < 2 {
		return "", fmt.Errorf("invalid CDX response format")
	}
	
	timestamp, ok := row[1].(string)
	if !ok {
		return "", fmt.Errorf("invalid timestamp in CDX response")
	}
	
	// Construct the Wayback URL
	waybackURL := fmt.Sprintf("http://web.archive.org/web/%s/%s", timestamp, originalURL)
	debugLog("Wayback URL: %s", waybackURL)
	
	return waybackURL, nil
}

func extractRedirectURL(redirectURL string) string {
	// Parse the URL to get query parameters
	parsedURL, err := url.Parse(redirectURL)
	if err != nil {
		return redirectURL
	}
	
	// Common redirect parameter names
	redirectParams := []string{
		"redirect", "redir", "next", "url", "u", "dest", 
		"destination", "forward", "return", "RelayState", 
		"goto", "callback", "continue", "target",
	}
	
	// Check each parameter
	query := parsedURL.Query()
	for _, param := range redirectParams {
		if value := query.Get(param); value != "" {
			// If the value looks like a URL, return it
			if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
				return value
			}
			// If it's a relative path, construct full URL
			if strings.HasPrefix(value, "/") {
				baseURL := fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host)
				return baseURL + value
			}
		}
	}
	
	// If no redirect parameter found, return original URL
	return redirectURL
}

func handleRequest(w http.ResponseWriter, r *http.Request) {
	// Check if this is a geocities.restorativland.org request
	isGeocitiesRequest := strings.Contains(r.Host, "geocities.restorativland.org") || r.Host == "geocities.restorativland.org"
	
	if isGeocitiesRequest {
		// Handle geocities.restorativland.org requests directly
		// Construct the target URL for geocities.restorativland.org (use HTTPS)
		targetURL, err := url.Parse("https://geocities.restorativland.org")
		if err != nil {
			http.Error(w, "Error parsing geocities.restorativland.org URL", 500)
			errorLog("Error parsing geocities.restorativland.org URL: %v", err)
			return
		}
		
		debugLog("Handling geocities request - Host: %s, Path: %s, Query: %s", r.Host, r.URL.Path, r.URL.RawQuery)
		debugLog("Target base URL: %s", targetURL.String())
		
		// Create a reverse proxy
	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	
	// Modify the request to match the target
	proxy.Director = func(req *http.Request) {
		// Set the scheme and host
		req.URL.Scheme = targetURL.Scheme
		req.URL.Host = targetURL.Host
		
		// Preserve the original path and query parameters
		req.URL.Path = r.URL.Path
		if req.URL.Path == "" {
			req.URL.Path = "/"
		}
		req.URL.RawQuery = r.URL.RawQuery
		
		// Set the Host header
		req.Host = targetURL.Host
		
		// Remove headers that might interfere
		req.Header.Del("Proxy-Connection")
		req.Header.Del("Proxy-Authorization")
		
		debugLog("Proxying to: %s://%s%s", req.URL.Scheme, req.URL.Host, req.URL.Path)
		if req.URL.RawQuery != "" {
			debugLog("With query: %s", req.URL.RawQuery)
		}
	}
	
	// Handle response modification to rewrite redirect URLs and modify HTML content
	proxy.ModifyResponse = func(resp *http.Response) error {
		// Check if it's a redirect response
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			// Check if there's a Location header
			if location := resp.Header.Get("Location"); location != "" {
				debugLog("Original redirect location: %s", location)
				
				// Parse the location URL
			locationURL, err := url.Parse(location)
			if err != nil {
					debugLog("Error parsing redirect location: %v", err)
					return nil // Continue with original response
				}
				
				// If it's redirecting to the same domain, ensure it goes through our proxy
				if locationURL.Host == "geocities.restorativland.org" {
					// Keep the same scheme (HTTPS) but ensure it goes through our proxy
					// We don't need to rewrite it since we're already using HTTPS
					debugLog("Redirect staying within geocities.restorativland.org domain")
				}
			} else {
				debugLog("301 response but no Location header found")
			}
		}
		
		// Check if it's HTML content and modify it to remove screenshots for better performance
		contentType := resp.Header.Get("Content-Type")
		if strings.Contains(contentType, "text/html") {
			// Read the body
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return err
			}
			
			// Convert to string
			html := string(body)
			
			// Remove screenshot images to improve performance on retro computers
			// Remove the entire card-image div which contains the screenshot
			re := regexp.MustCompile(`<div\s+class="card-image">.*?</div>`)
			html = re.ReplaceAllString(html, "<!-- Screenshot removed for performance -->")
			
			// Create a new body with modified content
			resp.Body = io.NopCloser(strings.NewReader(html))
			resp.ContentLength = int64(len(html))
			resp.Header.Set("Content-Length", fmt.Sprintf("%d", len(html)))
		}
		
		return nil
	}
		
		// Apply retry logic only to the proxy call
		var lastErr error
		var recorder *httptest.ResponseRecorder
		shouldRetry := false
		
		for attempt := 0; attempt < *maxRetries; attempt++ {
			if attempt > 0 {
				debugLog("Retrying proxy request (attempt %d/%d), waiting %v...", attempt+1, *maxRetries, *retryDelay)
				time.Sleep(*retryDelay)
				*retryDelay *= 2 // Exponential backoff
			}
			
			recorder = httptest.NewRecorder()
			
			// Call the proxy with retry logic
			func() {
				defer func() {
					if r := recover(); r != nil {
						lastErr = fmt.Errorf("proxy panic: %v", r)
					}
				}()
				proxy.ServeHTTP(recorder, r)
			}()
			
			resp := recorder.Result()
			
			debugLog("Geocities proxy response status: %d", resp.StatusCode)
			
			// HTTP 200-399 are all valid responses (including redirects)
			if resp.StatusCode >= 200 && resp.StatusCode < 400 {
				debugLog("Successfully proxying response with status %d", resp.StatusCode)
				// Log the Location header specifically if it exists
				if location := resp.Header.Get("Location"); location != "" {
					debugLog("Redirect location: %s", location)
				}
				// Success - copy response
				for k, v := range recorder.Header() {
					w.Header()[k] = v
				}
				w.WriteHeader(recorder.Code)
				io.Copy(w, recorder.Body)
				return
			}
			
			lastErr = fmt.Errorf("proxy returned status %d", resp.StatusCode)
			
			// Only retry on connection-related errors
			if resp.StatusCode == 502 || strings.Contains(resp.Status, "connection refused") {
				shouldRetry = true
				errorLog("Proxy request attempt %d failed with status %d (connection-related), will retry", attempt+1, resp.StatusCode)
				continue
			}
			
			// Other errors are not retryable
			errorLog("Proxy request attempt %d failed with status %d (not retryable)", attempt+1, resp.StatusCode)
			debugLog("Response headers: %v", resp.Header)
			// Log the Location header specifically if it exists
			if location := resp.Header.Get("Location"); location != "" {
				debugLog("Redirect location: %s", location)
			}
			break
		}
		
		// Handle final result
		if shouldRetry && lastErr != nil {
			errorLog("Proxy request failed after %d attempts: %v", *maxRetries, lastErr)
			http.Error(w, "Failed to connect to geocities.restorativland.org after "+strconv.Itoa(*maxRetries)+" attempts", 502)
		} else if recorder != nil {
			// Return last response
			debugLog("Returning final response")
			for k, v := range recorder.Header() {
				w.Header()[k] = v
			}
			w.WriteHeader(recorder.Code)
			io.Copy(w, recorder.Body)
		} else {
			errorLog("No response recorded: %v", lastErr)
			http.Error(w, "Error proxying request to geocities.restorativland.org: "+lastErr.Error(), 500)
		}
		
		return
	}
	
	// For regular requests, proxy to Wayback Machine
	originalURL := r.URL.String()
	
	// Check if this is already a Wayback Machine URL
	isWaybackURL := strings.HasPrefix(originalURL, "http://web.archive.org/web/")
	
	if !strings.HasPrefix(originalURL, "http") {
		originalURL = "http://" + r.Host + originalURL
	}
	
	debugLog("Original request: %s", originalURL)
	
	var waybackURL string
	var err error
	
	// If this is already a Wayback URL, we still need to check for redirects
	if isWaybackURL {
		// Extract the original URL from the Wayback URL
		parts := strings.Split(originalURL, "/")
		if len(parts) >= 7 {
			// Reconstruct the original URL
			originalPart := strings.Join(parts[6:], "/")
			// Check if this contains redirect parameters
			destinationURL := extractRedirectURL("http://" + originalPart)
			
			// If the destination is different, get the Wayback URL for it
			if destinationURL != "http://"+originalPart {
				waybackURL, err = getWaybackURL(destinationURL, *date)
				if err != nil {
					http.Error(w, "Error finding archived version: "+err.Error(), 500)
					errorLog("Error getting Wayback URL for %s: %v", destinationURL, err)
					return
				}
				debugLog("Redirecting to: %s", waybackURL)
			} else {
				// Use the existing Wayback URL
				waybackURL = originalURL
				debugLog("Using existing Wayback URL: %s", waybackURL)
			}
		} else {
			// Use the existing Wayback URL
			waybackURL = originalURL
			debugLog("Using existing Wayback URL: %s", waybackURL)
		}
	} else {
		// Check if this is a redirect URL and extract the destination
		destinationURL := extractRedirectURL(originalURL)
		
		// Get the Wayback URL for the destination
		waybackURL, err = getWaybackURL(destinationURL, *date)
		if err != nil {
			http.Error(w, "Error finding archived version: "+err.Error(), 500)
			errorLog("Error getting Wayback URL for %s: %v", destinationURL, err)
			return
		}
	}
	
	// Parse the Wayback URL
	targetURL, err := url.Parse(waybackURL)
	if err != nil {
		http.Error(w, "Error parsing Wayback URL", 500)
		errorLog("Error parsing Wayback URL %s: %v", waybackURL, err)
		return
	}
	
	// Create a reverse proxy
	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	
	// Modify the request to match the target
	proxy.Director = func(req *http.Request) {
		req.URL = targetURL
		req.Host = targetURL.Host
		req.URL.Scheme = targetURL.Scheme
		req.URL.Host = targetURL.Host
		
		// Remove headers that might interfere
		req.Header.Del("Proxy-Connection")
		req.Header.Del("Proxy-Authorization")
	}
	
	// Handle response modification for HTML content
	proxy.ModifyResponse = func(resp *http.Response) error {
		// Check if it's HTML content
		contentType := resp.Header.Get("Content-Type")
		if strings.Contains(contentType, "text/html") {
			// Read the body
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return err
			}
			
			// Convert to string and remove Wayback elements
			html := string(body)
			html = removeWaybackToolbar(html)
			
			// Create a new body with modified content
			resp.Body = io.NopCloser(strings.NewReader(html))
			resp.ContentLength = int64(len(html))
			resp.Header.Set("Content-Length", fmt.Sprintf("%d", len(html)))
		}
		return nil
	}
	
	// Apply retry logic only to the proxy call
	var lastErr error
	var recorder *httptest.ResponseRecorder
	shouldRetry := false
	
	for attempt := 0; attempt < *maxRetries; attempt++ {
		if attempt > 0 {
			debugLog("Retrying proxy request (attempt %d/%d), waiting %v...", attempt+1, *maxRetries, *retryDelay)
			time.Sleep(*retryDelay)
			*retryDelay *= 2 // Exponential backoff
		}
		
		recorder = httptest.NewRecorder()
		
		// Call the proxy with retry logic
		func() {
			defer func() {
				if r := recover(); r != nil {
					lastErr = fmt.Errorf("proxy panic: %v", r)
				}
			}()
			proxy.ServeHTTP(recorder, r)
		}()
		
		resp := recorder.Result()
		
		// HTTP 200-399 are all valid responses
		if resp.StatusCode >= 200 && resp.StatusCode < 400 {
			// Success - copy response
			for k, v := range recorder.Header() {
				w.Header()[k] = v
			}
			w.WriteHeader(recorder.Code)
			io.Copy(w, recorder.Body)
			return
		}
		
		lastErr = fmt.Errorf("proxy returned status %d", resp.StatusCode)
		
		// Only retry on connection-related errors
		if resp.StatusCode == 502 || strings.Contains(resp.Status, "connection refused") {
			shouldRetry = true
			errorLog("Proxy request attempt %d failed with status %d (connection-related), will retry", attempt+1, resp.StatusCode)
			continue
		}
		
		// Other errors are not retryable
		errorLog("Proxy request attempt %d failed with status %d (not retryable)", attempt+1, resp.StatusCode)
		break
	}
	
	// Handle final result
	if shouldRetry && lastErr != nil {
		errorLog("Proxy request failed after %d attempts: %v", *maxRetries, lastErr)
		http.Error(w, "Failed to connect to archived content after "+strconv.Itoa(*maxRetries)+" attempts", 502)
	} else if recorder != nil {
		// Return last response
		for k, v := range recorder.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(recorder.Code)
		io.Copy(w, recorder.Body)
	} else {
		http.Error(w, "Error proxying request: "+lastErr.Error(), 500)
	}
}

func main() {
	flag.Parse()
	
	if *date == "" {
		log.Fatal("Date parameter is required")
	}
	
	// Validate date format
	if len(*date) != 8 {
		log.Fatal("Date must be in YYYYMMDD format")
	}
	
	// Parse date to verify it's valid
	_, err := time.Parse("20060102", *date)
	if err != nil {
		log.Fatalf("Invalid date format: %v", err)
	}
	
	// Set up the proxy server
	http.HandleFunc("/", handleRequest)
	
	addr := fmt.Sprintf(":%s", *port)
	debugLog("Starting proxy server on port %s for date %s", *port, *date)
	
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal(err)
	}
}