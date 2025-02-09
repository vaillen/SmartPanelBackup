package backup

import (
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "time"
    "bytes"
)

// DBBackup handles database backup operations
type DBBackup struct {
    manager *BackupManager
}

// NewDBBackup creates a new database backup handler
func NewDBBackup(manager *BackupManager) *DBBackup {
    return &DBBackup{manager: manager}
}

// BackupDatabase performs a backup of the site's database
func (db *DBBackup) BackupDatabase(siteName, dbHost, dbName, dbUser, dbPass string) error {
    // Create database backup directory
    dbBackupDir := db.manager.getDBBackupDir(siteName)
    if err := os.MkdirAll(dbBackupDir, 0755); err != nil {
        return fmt.Errorf("failed to create database backup directory: %v", err)
    }

    // Generate backup filename with timestamp
    timestamp := time.Now().Format("2006-01-02_150405")
    backupFile := filepath.Join(dbBackupDir, fmt.Sprintf("db_%s.sql.gz", timestamp))

    // Create mysqldump command with error output capture
    cmd := exec.Command("mysqldump",
        "-h", dbHost,
        "-u", dbUser,
        fmt.Sprintf("-p%s", dbPass),
        "--quick",
        "--lock-tables=false",
        dbName)

    var stderr bytes.Buffer
    cmd.Stderr = &stderr

    // Create the backup file
    file, err := os.Create(backupFile)
    if err != nil {
        return fmt.Errorf("failed to create backup file: %v", err)
    }
    defer file.Close()

    // Create gzip command to compress the output
    gzip := exec.Command("gzip")
    gzip.Stdin, err = cmd.StdoutPipe()
    if err != nil {
        return fmt.Errorf("failed to create pipe: %v", err)
    }
    gzip.Stdout = file

    // Start gzip
    if err := gzip.Start(); err != nil {
        return fmt.Errorf("failed to start gzip: %v", err)
    }

    // Run mysqldump
    if err := cmd.Run(); err != nil {
        // Include MySQL error output in the error message
        return fmt.Errorf("failed to run mysqldump: %v, MySQL error: %s", err, stderr.String())
    }

    // Wait for gzip to finish
    if err := gzip.Wait(); err != nil {
        return fmt.Errorf("failed to finish gzip: %v", err)
    }

    fmt.Printf("Created database backup for %s at %s\n", siteName, backupFile)

    // Clean old backups
    if err := db.manager.cleanOldBackups(siteName, true); err != nil {
        return fmt.Errorf("failed to clean old backups: %v", err)
    }

    return nil
}
