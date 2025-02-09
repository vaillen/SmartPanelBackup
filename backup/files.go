package backup

import (
    "fmt"
    "os"
    "path/filepath"
    "time"
    "io"
    "archive/tar"
    "compress/gzip"
    "strings"
)

// FileBackup handles file backup operations
type FileBackup struct {
    manager *BackupManager
}

// NewFileBackup creates a new file backup handler
func NewFileBackup(manager *BackupManager) *FileBackup {
    return &FileBackup{manager: manager}
}

// compareWithLastBackup checks if files have changed since last backup
func (fb *FileBackup) compareWithLastBackup(siteName, sourceDir string) (bool, error) {
    // Get list of existing backups
    backupDir := filepath.Join(fb.manager.BaseDir, siteName)
    entries, err := os.ReadDir(backupDir)
    if err != nil {
        if os.IsNotExist(err) {
            return true, nil // No previous backups, need to create first one
        }
        return false, fmt.Errorf("failed to read backup directory: %v", err)
    }

    // Find latest backup
    var latestBackup string
    var latestTime time.Time
    for _, entry := range entries {
        if entry.IsDir() || !strings.HasPrefix(entry.Name(), "files_") {
            continue
        }
        
        // Parse timestamp from filename
        timeStr := strings.TrimPrefix(strings.TrimSuffix(entry.Name(), ".tar.gz"), "files_")
        backupTime, err := time.Parse("2006-01-02_150405", timeStr)
        if err != nil {
            continue
        }

        if latestBackup == "" || backupTime.After(latestTime) {
            latestBackup = entry.Name()
            latestTime = backupTime
        }
    }

    if latestBackup == "" {
        return true, nil // No valid backups found
    }

    // Create temporary directory for comparison inside backup directory
    tempDir := filepath.Join(backupDir, fmt.Sprintf("temp_%d", time.Now().UnixNano()))
    if err := os.MkdirAll(tempDir, 0755); err != nil {
        return false, fmt.Errorf("failed to create temp directory: %v", err)
    }
    defer os.RemoveAll(tempDir)

    // Extract latest backup
    latestBackupPath := filepath.Join(backupDir, latestBackup)
    if err := fb.extractArchive(latestBackupPath, tempDir); err != nil {
        return false, fmt.Errorf("failed to extract latest backup: %v", err)
    }

    // Compare directories
    changed := false
    err = filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
        if err != nil {
            return err
        }

        // Skip node_modules
        if info.IsDir() && info.Name() == "node_modules" {
            return filepath.SkipDir
        }

        // Skip symlinks
        if info.Mode()&os.ModeSymlink != 0 {
            return nil
        }

        // Get relative path
        relPath, err := filepath.Rel(sourceDir, path)
        if err != nil {
            return err
        }

        // Skip root directory
        if relPath == "." {
            return nil
        }

        // Get corresponding file in backup
        backupPath := filepath.Join(tempDir, relPath)
        backupInfo, err := os.Lstat(backupPath)
        if err != nil {
            if os.IsNotExist(err) {
                changed = true // File doesn't exist in backup
                return nil
            }
            return err
        }

        // Compare file types
        if info.Mode()&os.ModeType != backupInfo.Mode()&os.ModeType {
            changed = true
            return nil
        }

        // Compare modification times and sizes for regular files
        if !info.IsDir() {
            if info.ModTime().After(latestTime) || info.Size() != backupInfo.Size() {
                changed = true
                return nil
            }
        }

        return nil
    })

    if err != nil {
        return false, fmt.Errorf("failed to compare directories: %v", err)
    }

    return changed, nil
}

// extractArchive extracts a tar.gz archive to the specified directory
func (fb *FileBackup) extractArchive(archivePath, destDir string) error {
    file, err := os.Open(archivePath)
    if err != nil {
        return fmt.Errorf("failed to open archive: %v", err)
    }
    defer file.Close()

    gzr, err := gzip.NewReader(file)
    if err != nil {
        return fmt.Errorf("failed to create gzip reader: %v", err)
    }
    defer gzr.Close()

    tr := tar.NewReader(gzr)

    for {
        header, err := tr.Next()
        if err == io.EOF {
            break
        }
        if err != nil {
            return fmt.Errorf("failed to read tar header: %v", err)
        }

        // Skip symlinks
        if header.Typeflag == tar.TypeSymlink {
            continue
        }

        target := filepath.Join(destDir, header.Name)

        switch header.Typeflag {
        case tar.TypeDir:
            if err := os.MkdirAll(target, 0755); err != nil {
                return fmt.Errorf("failed to create directory: %v", err)
            }
        case tar.TypeReg:
            dir := filepath.Dir(target)
            if err := os.MkdirAll(dir, 0755); err != nil {
                return fmt.Errorf("failed to create directory: %v", err)
            }
            f, err := os.Create(target)
            if err != nil {
                return fmt.Errorf("failed to create file: %v", err)
            }
            if _, err := io.Copy(f, tr); err != nil {
                f.Close()
                return fmt.Errorf("failed to write file: %v", err)
            }
            f.Close()
        }
    }

    return nil
}

// BackupFiles creates a backup of the specified directory
func (fb *FileBackup) BackupFiles(siteName, sourceDir string) error {
    // Check if files have changed since last backup
    changed, err := fb.compareWithLastBackup(siteName, sourceDir)
    if err != nil {
        return fmt.Errorf("failed to compare with last backup: %v", err)
    }

    if !changed {
        fmt.Printf("No changes detected for %s, skipping backup\n", siteName)
        return nil
    }

    // Create backup directory
    backupDir := filepath.Join(fb.manager.BaseDir, siteName)
    if err := os.MkdirAll(backupDir, 0755); err != nil {
        return fmt.Errorf("failed to create backup directory: %v", err)
    }

    // Generate backup file name with timestamp
    timestamp := time.Now().Format("2006-01-02_150405")
    backupFile := filepath.Join(backupDir, fmt.Sprintf("files_%s.tar.gz", timestamp))

    // Create archive
    if err := fb.createArchive(sourceDir, backupFile); err != nil {
        return err
    }

    fmt.Printf("Created backup for %s at %s\n", siteName, backupFile)

    // Clean old backups
    return fb.manager.cleanOldBackups(siteName, false)
}

// createArchive creates a tar.gz archive of the source directory
func (fb *FileBackup) createArchive(sourceDir, targetFile string) error {
    // Create target file
    file, err := os.Create(targetFile)
    if err != nil {
        return fmt.Errorf("failed to create archive file: %v", err)
    }
    defer file.Close()

    // Create gzip writer
    gw := gzip.NewWriter(file)
    defer gw.Close()

    // Create tar writer
    tw := tar.NewWriter(gw)
    defer tw.Close()

    // Walk through source directory
    err = filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
        if err != nil {
            return err
        }

        // Skip node_modules directory
        if info.IsDir() && info.Name() == "node_modules" {
            return filepath.SkipDir
        }

        // Skip symlinks
        if info.Mode()&os.ModeSymlink != 0 {
            return nil
        }

        // Get relative path
        relPath, err := filepath.Rel(sourceDir, path)
        if err != nil {
            return fmt.Errorf("failed to get relative path: %v", err)
        }

        // Skip root directory
        if relPath == "." {
            return nil
        }

        // Create tar header
        header, err := tar.FileInfoHeader(info, "")
        if err != nil {
            return fmt.Errorf("failed to create tar header: %v", err)
        }

        // Update header name to use relative path
        header.Name = relPath

        // Write header
        if err := tw.WriteHeader(header); err != nil {
            return fmt.Errorf("failed to write tar header: %v", err)
        }

        // If this is a directory, continue to next file
        if info.IsDir() {
            return nil
        }

        // Open and copy file content
        file, err := os.Open(path)
        if err != nil {
            return fmt.Errorf("failed to open file: %v", err)
        }
        defer file.Close()

        if _, err := io.Copy(tw, file); err != nil {
            return fmt.Errorf("failed to write file content: %v", err)
        }

        return nil
    })

    if err != nil {
        return fmt.Errorf("failed to create backup archive: %v", err)
    }

    return nil
}
