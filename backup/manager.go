package backup

import (
    "fmt"
    "os"
    "path/filepath"
    "sort"
    "strings"
    "strconv"
    "time"
)

const (
    // Default maximum number of file backups to keep per site
    DefaultMaxFileBackups = 5
    // Default maximum number of database backups to keep per site
    DefaultMaxDBBackups   = 20
)

// BackupManager handles backup operations and rotation
type BackupManager struct {
    BaseDir string
    MaxFileBackups int
    MaxDBBackups int
}

// NewBackupManager creates a new backup manager instance
func NewBackupManager(baseDir string) (*BackupManager, error) {
    // Create base backup directory if it doesn't exist
    if err := os.MkdirAll(baseDir, 0755); err != nil {
        return nil, fmt.Errorf("failed to create backup directory: %v", err)
    }

    // Get backup limits from environment
    var maxFiles, maxDB int
    if strings.Contains(baseDir, "-ssh") {
        // Remote backup settings
        maxFiles = getEnvInt("REMOTE_MAX_FILE_BACKUPS", DefaultMaxFileBackups)
        maxDB = getEnvInt("REMOTE_MAX_DB_BACKUPS", DefaultMaxDBBackups)
    } else {
        // Local backup settings
        maxFiles = getEnvInt("LOCAL_MAX_FILE_BACKUPS", DefaultMaxFileBackups)
        maxDB = getEnvInt("LOCAL_MAX_DB_BACKUPS", DefaultMaxDBBackups)
    }

    return &BackupManager{
        BaseDir: baseDir,
        MaxFileBackups: maxFiles,
        MaxDBBackups: maxDB,
    }, nil
}

// getEnvInt gets an integer value from environment with default
func getEnvInt(key string, defaultVal int) int {
    if val := os.Getenv(key); val != "" {
        if i, err := strconv.Atoi(val); err == nil {
            return i
        }
    }
    return defaultVal
}

// getSiteBackupDir returns the backup directory path for a specific site
func (bm *BackupManager) getSiteBackupDir(siteName string) string {
    return filepath.Join(bm.BaseDir, siteName)
}

// getDBBackupDir returns the database backup directory path for a specific site
func (bm *BackupManager) getDBBackupDir(siteName string) string {
    return filepath.Join(bm.getSiteBackupDir(siteName), "database")
}

// getLatestBackup returns the path to the latest backup for a site
// Returns empty string if no backup exists
func (bm *BackupManager) getLatestBackup(siteName string) (string, error) {
    backupDir := bm.getSiteBackupDir(siteName)
    entries, err := os.ReadDir(backupDir)
    if err != nil {
        if os.IsNotExist(err) {
            return "", nil
        }
        return "", err
    }

    var latestTime time.Time
    var latestPath string

    for _, entry := range entries {
        if entry.IsDir() && strings.HasPrefix(entry.Name(), "files_") {
            timeStr := strings.TrimPrefix(entry.Name(), "files_")
            t, err := time.Parse("2006-01-02_150405", timeStr)
            if err != nil {
                continue
            }
            if t.After(latestTime) {
                latestTime = t
                latestPath = filepath.Join(backupDir, entry.Name())
            }
        }
    }

    return latestPath, nil
}

// cleanOldBackups removes old backups exceeding the maximum limit
// Uses rotation strategy: keeps most recent backups and removes the oldest ones
func (bm *BackupManager) cleanOldBackups(siteName string, isDatabase bool) error {
    var pattern string
    var maxBackups int
    
    if isDatabase {
        pattern = "db_*.sql.gz"
        maxBackups = bm.MaxDBBackups
    } else {
        pattern = "files_*.tar.gz"
        maxBackups = bm.MaxFileBackups
    }

    // Get backup directory
    backupDir := filepath.Join(bm.BaseDir, siteName)
    if isDatabase {
        backupDir = filepath.Join(backupDir, "database")
    }

    // List all backups
    matches, err := filepath.Glob(filepath.Join(backupDir, pattern))
    if err != nil {
        return fmt.Errorf("failed to list backups: %v", err)
    }

    // If we don't have more than max backups, no need to clean
    if len(matches) <= maxBackups {
        return nil
    }

    // Sort backups by modification time (newest first)
    sort.Slice(matches, func(i, j int) bool {
        iInfo, _ := os.Stat(matches[i])
        jInfo, _ := os.Stat(matches[j])
        return iInfo.ModTime().After(jInfo.ModTime())
    })

    // Remove old backups
    for _, file := range matches[maxBackups:] {
        if err := os.Remove(file); err != nil {
            return fmt.Errorf("failed to remove old backup %s: %v", file, err)
        }
    }

    return nil
}
