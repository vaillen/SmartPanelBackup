package config

import (
    "os"
    "path/filepath"
    "regexp"
    "strings"
)

// findEnvFile searches for .env file in the given directory and its parent directories
func findEnvFile(startPath string) (string, error) {
    // Convert potential relative path to absolute
    absPath, err := filepath.Abs(startPath)
    if err != nil {
        return "", err
    }

    // List of possible .env locations relative to the document root
    possibleLocations := []string{
        ".env",                    // в той же директории
        "../.env",                 // на уровень выше
        "../../.env",              // на два уровня выше
        "../../../.env",           // на три уровня выше
        "public/.env",             // в public директории
        "public_html/.env",        // в public_html директории
        "html/.env",              // в html директории
        "app/.env",               // в app директории
        "laravel/.env",           // в laravel директории
    }

    // First check if the path is a file (might be direct path to .env)
    if strings.HasSuffix(absPath, ".env") {
        if _, err := os.Stat(absPath); err == nil {
            return absPath, nil
        }
    }

    // If it's a directory, check all possible locations
    baseDir := absPath
    if !strings.HasSuffix(baseDir, "/") {
        baseDir = filepath.Dir(baseDir)
    }

    for _, loc := range possibleLocations {
        envPath := filepath.Join(baseDir, loc)
        if _, err := os.Stat(envPath); err == nil {
            return envPath, nil
        }
    }

    return "", os.ErrNotExist
}

// ParseLaravelEnv reads the Laravel .env file and extracts database credentials
func ParseLaravelEnv(documentRoot string) (string, string, string, string, error) {
    // Find .env file
    envPath, err := findEnvFile(documentRoot)
    if err != nil {
        // Return empty strings without error if file not found
        return "", "", "", "", nil
    }

    content, err := os.ReadFile(envPath)
    if err != nil {
        // Return empty strings without error if can't read file
        return "", "", "", "", nil
    }

    envContent := string(content)

    // Extract values with proper quote handling
    dbHost := extractEnvValue(envContent, "DB_HOST")
    dbName := extractEnvValue(envContent, "DB_DATABASE")
    dbUser := extractEnvValue(envContent, "DB_USERNAME")
    dbPass := extractEnvValue(envContent, "DB_PASSWORD")

    return dbHost, dbName, dbUser, dbPass, nil
}

func extractEnvValue(content, key string) string {
    re := regexp.MustCompile(`(?m)^` + key + `=(?:"([^"]*)"|'([^']*)'|([^\n\r]*))`)
    match := re.FindStringSubmatch(content)
    if len(match) > 1 {
        // Check each capture group and return the first non-empty one
        for i := 1; i < len(match); i++ {
            if match[i] != "" {
                return strings.TrimSpace(match[i])
            }
        }
    }
    return ""
}
