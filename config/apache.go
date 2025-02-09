package config

import (
    "bufio"
    "os"
    "strings"
    "regexp"
)

// ParseApacheConfig reads the Apache configuration file and extracts ServerName and DocumentRoot
func ParseApacheConfig(configPath string) (map[string]string, error) {
    file, err := os.Open(configPath)
    if err != nil {
        return nil, err
    }
    defer file.Close()

    sites := make(map[string]string)
    scanner := bufio.NewScanner(file)
    
    var currentServerName string
    
    // Compile regular expressions
    serverNameRegex := regexp.MustCompile(`(?i)^\s*ServerName\s+(.+)`)
    documentRootRegex := regexp.MustCompile(`(?i)^\s*DocumentRoot\s+(.+)`)

    for scanner.Scan() {
        line := strings.TrimSpace(scanner.Text())
        
        // Skip comments and empty lines
        if strings.HasPrefix(line, "#") || line == "" {
            continue
        }

        // Check for ServerName
        if matches := serverNameRegex.FindStringSubmatch(line); len(matches) > 1 {
            currentServerName = strings.TrimSpace(matches[1])
            continue
        }

        // Check for DocumentRoot if we have a ServerName
        if currentServerName != "" {
            if matches := documentRootRegex.FindStringSubmatch(line); len(matches) > 1 {
                documentRoot := strings.TrimSpace(matches[1])
                // Remove quotes if present
                documentRoot = strings.Trim(documentRoot, `"'`)
                sites[currentServerName] = documentRoot
            }
        }
    }

    if err := scanner.Err(); err != nil {
        return nil, err
    }

    return sites, nil
}
