package main

import (
    "fmt"
    "log"
    "sync"
    "os"
    "github.com/joho/godotenv"
    "laravel-backup-tool/config"
    "laravel-backup-tool/models"
    "laravel-backup-tool/backup"
)

// BackupResult stores the result of a backup operation
type BackupResult struct {
    SiteName string
    Error    error
    Type     string // "file" or "database"
}

func main() {
    // Load environment variables
    if err := godotenv.Load(); err != nil {
        log.Printf("Warning: .env file not found, using default settings")
    }

    // First, perform local backups
    fmt.Println("Starting local backups...")
    if err := performLocalBackups(); err != nil {
        log.Printf("Error during local backups: %v", err)
    }

    // Then, if enabled, perform remote backups
    if os.Getenv("REMOTE_BACKUP_ENABLED") == "true" {
        fmt.Println("\nStarting remote backups...")
        if err := performRemoteBackups(); err != nil {
            log.Printf("Error during remote backups: %v", err)
        }
    }
}

func performLocalBackups() error {
    // Parse Apache configuration file to get site information
    sites, err := config.ParseApacheConfig("/etc/apache2/conf/httpd.conf")
    if err != nil {
        return fmt.Errorf("error parsing Apache config: %v", err)
    }

    // Initialize backup manager with the script-specific backup directory
    backupManager, err := backup.NewBackupManager("/laravel-backup-script")
    if err != nil {
        return fmt.Errorf("error initializing backup manager: %v", err)
    }

    // Initialize backup handlers for files and databases
    fileBackup := backup.NewFileBackup(backupManager)
    dbBackup := backup.NewDBBackup(backupManager)

    // Store information about all sites
    var siteInfos []models.Site

    // Channel for collecting backup results
    resultChan := make(chan BackupResult)
    
    // WaitGroup to track all running goroutines
    var wg sync.WaitGroup

    // Process each site: get Laravel database credentials and perform backups
    for serverName, documentRoot := range sites {
        site := models.Site{
            ServerName:   serverName,
            DocumentRoot: documentRoot,
        }

        // Parse Laravel .env file for database credentials
        dbHost, dbName, dbUser, dbPass, _ := config.ParseLaravelEnv(documentRoot)
        site.DatabaseHost = dbHost
        site.DatabaseName = dbName
        site.DatabaseUser = dbUser
        site.DatabasePass = dbPass

        siteInfos = append(siteInfos, site)

        // Start file backup in a goroutine
        wg.Add(1)
        go func(site models.Site) {
            defer wg.Done()
            err := fileBackup.BackupFiles(site.ServerName, site.DocumentRoot)
            resultChan <- BackupResult{
                SiteName: site.ServerName,
                Error:    err,
                Type:     "file",
            }
        }(site)

        // Start database backup in a goroutine if credentials are available
        if dbHost != "" && dbName != "" && dbUser != "" && dbPass != "" {
            wg.Add(1)
            go func(site models.Site) {
                defer wg.Done()
                err := dbBackup.BackupDatabase(site.ServerName, site.DatabaseHost, 
                    site.DatabaseName, site.DatabaseUser, site.DatabasePass)
                resultChan <- BackupResult{
                    SiteName: site.ServerName,
                    Error:    err,
                    Type:     "database",
                }
            }(site)
        }
    }

    // Start a goroutine to close result channel when all backups are done
    go func() {
        wg.Wait()
        close(resultChan)
    }()

    // Collect and display backup results
    fmt.Println("\nLocal Backup Results:")
    fmt.Println("-------------------")
    
    for result := range resultChan {
        if result.Error != nil {
            log.Printf("Warning: Failed to backup %s (%s): %v", 
                result.SiteName, result.Type, result.Error)
        } else {
            fmt.Printf("Successfully backed up %s (%s)\n", 
                result.SiteName, result.Type)
        }
    }

    // Display information about all found sites
    fmt.Println("\nFound sites:")
    for _, site := range siteInfos {
        fmt.Printf("\nSite: %s\n", site.ServerName)
        fmt.Printf("Document Root: %s\n", site.DocumentRoot)
        
        // Display database information only if available
        if site.DatabaseHost != "" || site.DatabaseName != "" || site.DatabaseUser != "" || site.DatabasePass != "" {
            fmt.Printf("Database Host: %s\n", site.DatabaseHost)
            fmt.Printf("Database Name: %s\n", site.DatabaseName)
            fmt.Printf("Database User: %s\n", site.DatabaseUser)
            fmt.Printf("Database Password: %s\n", site.DatabasePass)
        } else {
            fmt.Println("No database configuration found")
        }
        fmt.Println("-------------------")
    }

    return nil
}

func performRemoteBackups() error {
    // Get SSH configuration from environment
    sshConfig := &backup.SSHConfig{
        Host:     os.Getenv("SSH_HOST"),
        User:     os.Getenv("SSH_USER"),
        Port:     os.Getenv("SSH_PORT"),
        KeyPath:  os.Getenv("SSH_KEY_PATH"),
        Password: os.Getenv("SSH_PASSWORD"),
    }

    // Validate SSH configuration
    if sshConfig.Host == "" || sshConfig.User == "" || 
       (sshConfig.KeyPath == "" && sshConfig.Password == "") {
        return fmt.Errorf("incomplete SSH configuration")
    }

    // Initialize SSH backup
    sshBackup, err := backup.NewSSHBackup(sshConfig)
    if err != nil {
        return fmt.Errorf("failed to initialize SSH backup: %v", err)
    }
    defer sshBackup.Close()

    // Perform remote backups
    if err := sshBackup.BackupRemoteSites(); err != nil {
        return fmt.Errorf("failed to perform remote backups: %v", err)
    }

    return nil
}
