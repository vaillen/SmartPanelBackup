package backup

import (
    "fmt"
    "os"
    "path/filepath"
    "strings"
    "golang.org/x/crypto/ssh"
    "io/ioutil"
    "time"
    "os/exec"
    "strconv"
)

const maxConcurrentSessions = 5 // Maximum number of concurrent SSH sessions

// SSHConfig holds SSH connection settings
type SSHConfig struct {
    Host     string
    User     string
    Port     string
    KeyPath  string
    Password string
}

// RemoteSite represents a Laravel site on the remote server
type RemoteSite struct {
    ServerName   string
    DocumentRoot string
    EnvContent  string
}

// SSHBackup handles remote server backup operations
type SSHBackup struct {
    config  *SSHConfig
    client  *ssh.Client
    manager *BackupManager
    sessionPool      chan *ssh.Session
    maxSessions     int
}

// NewSSHBackup creates a new SSH backup handler
func NewSSHBackup(config *SSHConfig) (*SSHBackup, error) {
    fmt.Println("Initializing SSH backup handler...")
    var authMethods []ssh.AuthMethod

    if config.KeyPath != "" {
        fmt.Printf("Using SSH key: %s\n", config.KeyPath)
        key, err := ioutil.ReadFile(config.KeyPath)
        if err != nil {
            return nil, fmt.Errorf("unable to read private key: %v", err)
        }

        signer, err := ssh.ParsePrivateKey(key)
        if err != nil {
            return nil, fmt.Errorf("unable to parse private key: %v", err)
        }
        authMethods = append(authMethods, ssh.PublicKeys(signer))
    }

    if config.Password != "" {
        fmt.Println("Using password authentication")
        authMethods = append(authMethods, ssh.Password(config.Password))
    }

    sshConfig := &ssh.ClientConfig{
        User: config.User,
        Auth: authMethods,
        HostKeyCallback: ssh.InsecureIgnoreHostKey(),
        Timeout: 30 * time.Second,
    }

    fmt.Printf("Connecting to SSH server %s:%s...\n", config.Host, config.Port)
    client, err := ssh.Dial("tcp", fmt.Sprintf("%s:%s", config.Host, config.Port), sshConfig)
    if err != nil {
        return nil, fmt.Errorf("unable to connect to SSH server: %v", err)
    }
    fmt.Println("Successfully connected to SSH server")

    // Initialize backup manager
    fmt.Println("Initializing backup manager...")
    manager, err := NewBackupManager("/laravel-backup-script-ssh")
    if err != nil {
        client.Close()
        return nil, fmt.Errorf("failed to initialize backup manager: %v", err)
    }

    sb := &SSHBackup{
        config:  config,
        client:  client,
        manager: manager,
        sessionPool: make(chan *ssh.Session, 10), // Start with 10 sessions, will adjust dynamically
        maxSessions: 10,
    }

    // Initialize remote environment and test session capacity
    if err := sb.initializeEnvironment(); err != nil {
        client.Close()
        return nil, fmt.Errorf("failed to initialize environment: %v", err)
    }

    return sb, nil
}

// initializeEnvironment sets up the remote environment and tests session capacity
func (sb *SSHBackup) initializeEnvironment() error {
    fmt.Println("Initializing remote environment...")
    
    // Create backup directory
    session, err := sb.client.NewSession()
    if err != nil {
        return fmt.Errorf("failed to create session: %v", err)
    }
    defer session.Close()

    if err := session.Run("mkdir -p ~/laravel-backup-temp"); err != nil {
        return fmt.Errorf("failed to create backup directory: %v", err)
    }

    // Test session capacity
    fmt.Println("Testing SSH session capacity...")
    var sessions []*ssh.Session
    for i := 0; i < 20; i++ { // Try up to 20 sessions
        session, err := sb.client.NewSession()
        if err != nil {
            sb.maxSessions = len(sessions)
            fmt.Printf("Maximum SSH sessions: %d\n", sb.maxSessions)
            break
        }
        sessions = append(sessions, session)
    }

    // Close test sessions
    for _, s := range sessions {
        s.Close()
    }

    // Create session pool
    sb.sessionPool = make(chan *ssh.Session, sb.maxSessions)
    for i := 0; i < sb.maxSessions; i++ {
        session, err := sb.client.NewSession()
        if err != nil {
            continue
        }
        sb.sessionPool <- session
    }

    return nil
}

// getSession gets a session from the pool or creates a new one
func (sb *SSHBackup) getSession() (*ssh.Session, error) {
    select {
    case session := <-sb.sessionPool:
        return session, nil
    default:
        // If pool is empty, create new session
        return sb.client.NewSession()
    }
}

// releaseSession returns a session to the pool or closes it
func (sb *SSHBackup) releaseSession(session *ssh.Session) {
    if session == nil {
        return
    }

    select {
    case sb.sessionPool <- session:
        // Session returned to pool
    default:
        // Pool is full, close session
        session.Close()
    }
}

// Close closes all sessions and connections
func (sb *SSHBackup) Close() error {
    // Close all sessions in pool
    for {
        select {
        case session := <-sb.sessionPool:
            session.Close()
        default:
            return sb.client.Close()
        }
    }
}

// SiteInfo holds all information about a site needed for backup
type SiteInfo struct {
    ServerName   string
    DocumentRoot string
    DBHost      string
    DBName      string
    DBUser      string
    DBPass      string
}

// gatherSiteInfo collects all site information in one session
func (sb *SSHBackup) gatherSiteInfo() ([]SiteInfo, error) {
    fmt.Println("Gathering site information...")

    // Try to find Apache config directory
    fmt.Println("Looking for Apache configuration...")
    session, err := sb.getSession()
    if err != nil {
        return nil, fmt.Errorf("failed to create session: %v", err)
    }
    findCmd := `find /etc -type f -name "httpd*.conf" 2>/dev/null || find /etc/apache2 -type f -name "*.conf" 2>/dev/null`
    output, err := session.CombinedOutput(findCmd)
    sb.releaseSession(session)
    if err != nil {
        fmt.Printf("Warning: failed to find Apache configs: %v\n", err)
    }

    configFiles := strings.Split(strings.TrimSpace(string(output)), "\n")
    if len(configFiles) == 0 {
        // Try common locations
        configFiles = []string{
            "/etc/apache2/apache2.conf",
            "/etc/apache2/httpd.conf",
            "/etc/httpd/conf/httpd.conf",
            "/etc/apache2/sites-enabled/*",
        }
    }

    // Remove duplicates from configFiles
    seen := make(map[string]bool)
    var uniqueConfigs []string
    for _, file := range configFiles {
        if !seen[file] && file != "" {
            seen[file] = true
            uniqueConfigs = append(uniqueConfigs, file)
        }
    }
    configFiles = uniqueConfigs

    fmt.Printf("Found config files: %v\n", configFiles)

    // Parse configurations
    sitesMap := make(map[string]SiteInfo)
    var currentSite SiteInfo

    // Read each config file
    for _, configFile := range configFiles {
        if strings.Contains(configFile, "*") {
            // Handle wildcards
            session, err := sb.getSession()
            if err != nil {
                continue
            }
            output, err := session.CombinedOutput(fmt.Sprintf("ls %s 2>/dev/null", configFile))
            sb.releaseSession(session)
            if err != nil {
                continue
            }
            // Add expanded files to the list
            for _, file := range strings.Split(strings.TrimSpace(string(output)), "\n") {
                if file != "" && !seen[file] {
                    seen[file] = true
                    configFiles = append(configFiles, file)
                }
            }
            continue
        }

        // Read config file
        session, err := sb.getSession()
        if err != nil {
            fmt.Printf("Warning: failed to create session for %s: %v\n", configFile, err)
            continue
        }
        output, err := session.CombinedOutput(fmt.Sprintf("cat %s 2>/dev/null", configFile))
        sb.releaseSession(session)
        if err != nil {
            fmt.Printf("Warning: failed to read config %s: %v\n", configFile, err)
            continue
        }

        // Parse file content
        lines := strings.Split(string(output), "\n")
        for _, line := range lines {
            line = strings.TrimSpace(line)

            if strings.HasPrefix(line, "ServerName") {
                parts := strings.Fields(line)
                if len(parts) >= 2 {
                    currentSite.ServerName = parts[1]
                }
            } else if strings.HasPrefix(line, "DocumentRoot") {
                parts := strings.Fields(line)
                if len(parts) >= 2 {
                    currentSite.DocumentRoot = strings.Trim(parts[1], "\"")
                    if currentSite.ServerName != "" {
                        // Try to read .env file
                        envSession, err := sb.getSession()
                        if err == nil {
                            envCmd := fmt.Sprintf("cat %s/.env 2>/dev/null", currentSite.DocumentRoot)
                            envOutput, err := envSession.CombinedOutput(envCmd)
                            sb.releaseSession(envSession)
                            if err == nil {
                                // Parse .env file for database credentials
                                envContent := string(envOutput)
                                for _, line := range strings.Split(envContent, "\n") {
                                    line = strings.TrimSpace(line)
                                    if strings.HasPrefix(line, "DB_HOST=") {
                                        currentSite.DBHost = strings.TrimPrefix(line, "DB_HOST=")
                                    } else if strings.HasPrefix(line, "DB_DATABASE=") {
                                        currentSite.DBName = strings.TrimPrefix(line, "DB_DATABASE=")
                                    } else if strings.HasPrefix(line, "DB_USERNAME=") {
                                        currentSite.DBUser = strings.TrimPrefix(line, "DB_USERNAME=")
                                    } else if strings.HasPrefix(line, "DB_PASSWORD=") {
                                        currentSite.DBPass = strings.TrimPrefix(line, "DB_PASSWORD=")
                                    }
                                }
                            }
                        }

                        // Only add site if it's not already in the map with the same DocumentRoot
                        key := fmt.Sprintf("%s:%s", currentSite.ServerName, currentSite.DocumentRoot)
                        if _, exists := sitesMap[key]; !exists {
                            sitesMap[key] = currentSite
                            fmt.Printf("Found site: %s at %s\n", currentSite.ServerName, currentSite.DocumentRoot)
                        }
                        currentSite = SiteInfo{} // Reset for next site
                    }
                }
            }
        }
    }

    // Convert map to slice
    var sites []SiteInfo
    for _, site := range sitesMap {
        sites = append(sites, site)
    }

    fmt.Printf("Found %d unique sites\n", len(sites))
    return sites, nil
}

// BackupRemoteSites performs backup of all sites on the remote server
func (sb *SSHBackup) BackupRemoteSites() error {
    // Gather all site information first
    sites, err := sb.gatherSiteInfo()
    if err != nil {
        return fmt.Errorf("failed to gather site information: %v", err)
    }

    // Clean existing files in temp directory
    fmt.Println("Cleaning temporary directory...")
    err = sb.runCommand("rm -rf ~/laravel-backup-temp/* && mkdir -p ~/laravel-backup-temp")
    if err != nil {
        return fmt.Errorf("failed to clean remote temp directory: %v", err)
    }

    // Backup each site sequentially
    for _, site := range sites {
        fmt.Printf("Starting backup check for %s...\n", site.ServerName)
        
        // Create local backup directory
        localDir := filepath.Join(sb.manager.BaseDir, site.ServerName)
        if err := os.MkdirAll(localDir, 0755); err != nil {
            fmt.Printf("Error creating local directory for %s: %v\n", site.ServerName, err)
            continue
        }

        // Check if we have today's backup already
        today := time.Now().Format("2006-01-02")
        hasBackupToday := false
        
        // Check for existing backups
        files, err := os.ReadDir(localDir)
        if err == nil {
            for _, file := range files {
                if strings.Contains(file.Name(), today) {
                    hasBackupToday = true
                    break
                }
            }
        }

        if hasBackupToday {
            fmt.Printf("Backup for %s already exists today, skipping...\n", site.ServerName)
            continue
        }

        // Check for changes on remote server
        fmt.Printf("Checking for changes in %s...\n", site.ServerName)
        
        // Get last modification time using find
        cmd := fmt.Sprintf("find %s -type f -mtime -1 -not -path '*/\\.*' -not -path '*/node_modules/*' | wc -l", site.DocumentRoot)
        session, err := sb.client.NewSession()
        if err != nil {
            fmt.Printf("Error creating session for %s: %v\n", site.ServerName, err)
            continue
        }
        
        output, err := session.CombinedOutput(cmd)
        session.Close()
        
        if err != nil {
            fmt.Printf("Error checking for changes in %s: %v\n", site.ServerName, err)
            continue
        }

        changedFiles, err := strconv.Atoi(strings.TrimSpace(string(output)))
        if err != nil {
            fmt.Printf("Error parsing changed files count for %s: %v\n", site.ServerName, err)
            continue
        }

        if changedFiles == 0 {
            fmt.Printf("No changes detected in %s, skipping...\n", site.ServerName)
            continue
        }

        fmt.Printf("Found %d changed files in %s, creating backup...\n", changedFiles, site.ServerName)

        // Create site backup directory
        siteDir := fmt.Sprintf("~/laravel-backup-temp/%s", site.ServerName)
        err = sb.runCommand(fmt.Sprintf("mkdir -p %s", siteDir))
        if err != nil {
            fmt.Printf("Error creating directory for %s: %v\n", site.ServerName, err)
            continue
        }

        // Backup files
        fmt.Printf("Creating file backup for %s...\n", site.ServerName)
        timestamp := time.Now().Format("2006-01-02_150405")
        cmd = fmt.Sprintf("cd %s && tar --exclude='./node_modules' -czf %s/files.tar.gz .", 
            site.DocumentRoot, siteDir)
        err = sb.runCommand(cmd)
        if err != nil {
            fmt.Printf("Error backing up files for %s: %v\n", site.ServerName, err)
            continue
        }

        // Copy files backup to local
        fmt.Printf("Copying files backup for %s to local machine...\n", site.ServerName)
        localBackupPath := filepath.Join(localDir, fmt.Sprintf("files_%s.tar.gz", timestamp))
        err = sb.copyFileFromRemote(
            fmt.Sprintf("%s/files.tar.gz", siteDir), 
            localBackupPath,
        )
        if err != nil {
            fmt.Printf("Error copying files backup for %s: %v\n", site.ServerName, err)
            continue
        }

        // Try to read .env file
        fmt.Printf("Reading .env for %s...\n", site.ServerName)
        session, err = sb.client.NewSession()
        if err != nil {
            fmt.Printf("Error creating session for %s: %v\n", site.ServerName, err)
            continue
        }
        envOutput, _ := session.CombinedOutput(fmt.Sprintf("cat %s/.env", site.DocumentRoot))
        session.Close()

        // Parse .env file for database credentials and backup if available
        if len(envOutput) > 0 {
            envContent := string(envOutput)
            var dbHost, dbName, dbUser, dbPass string
            for _, line := range strings.Split(envContent, "\n") {
                line = strings.TrimSpace(line)
                if strings.HasPrefix(line, "DB_HOST=") {
                    dbHost = strings.TrimPrefix(line, "DB_HOST=")
                } else if strings.HasPrefix(line, "DB_DATABASE=") {
                    dbName = strings.TrimPrefix(line, "DB_DATABASE=")
                } else if strings.HasPrefix(line, "DB_USERNAME=") {
                    dbUser = strings.TrimPrefix(line, "DB_USERNAME=")
                } else if strings.HasPrefix(line, "DB_PASSWORD=") {
                    dbPass = strings.TrimPrefix(line, "DB_PASSWORD=")
                }
            }

            // Backup database if credentials found
            if dbName != "" && dbUser != "" {
                fmt.Printf("Creating database backup for %s...\n", site.ServerName)
                cmd := fmt.Sprintf("mysqldump -h%s -u%s -p%s --quick --lock-tables=false %s | gzip > %s/db.sql.gz",
                    dbHost, dbUser, dbPass, dbName, siteDir)
                err = sb.runCommand(cmd)
                if err != nil {
                    fmt.Printf("Error backing up database for %s: %v\n", site.ServerName, err)
                } else {
                    // Only try to copy database backup if it was created successfully
                    fmt.Printf("Copying database backup for %s to local machine...\n", site.ServerName)
                    localDBPath := filepath.Join(localDir, fmt.Sprintf("db_%s.sql.gz", timestamp))
                    err = sb.copyFileFromRemote(
                        fmt.Sprintf("%s/db.sql.gz", siteDir),
                        localDBPath,
                    )
                    if err != nil {
                        fmt.Printf("Error copying database backup for %s: %v\n", site.ServerName, err)
                    }
                }
            }
        }

        // Clean old backups
        if err := sb.manager.cleanOldBackups(site.ServerName, false); err != nil {
            fmt.Printf("Warning: failed to clean old file backups for %s: %v\n", site.ServerName, err)
        }
        if err := sb.manager.cleanOldBackups(site.ServerName, true); err != nil {
            fmt.Printf("Warning: failed to clean old database backups for %s: %v\n", site.ServerName, err)
        }

        fmt.Printf("Successfully backed up %s\n", site.ServerName)
    }

    // Clean up temp directory
    fmt.Println("Cleaning up temporary directory...")
    err = sb.runCommand("rm -rf ~/laravel-backup-temp/*")
    if err != nil {
        fmt.Printf("Warning: failed to clean remote temp directory: %v\n", err)
    }

    return nil
}

// compareBackups compares two backup archives
func compareBackups(newBackup, oldBackup string) (bool, error) {
    // Создаем временные директории для распаковки
    tmpNew := newBackup + ".tmp"
    tmpOld := oldBackup + ".tmp"
    
    // Создаем директории
    if err := os.MkdirAll(tmpNew, 0755); err != nil {
        return false, err
    }
    if err := os.MkdirAll(tmpOld, 0755); err != nil {
        os.RemoveAll(tmpNew)
        return false, err
    }
    
    defer os.RemoveAll(tmpNew)
    defer os.RemoveAll(tmpOld)

    // Распаковываем архивы
    cmdNew := exec.Command("tar", "xzf", newBackup, "-C", tmpNew)
    cmdOld := exec.Command("tar", "xzf", oldBackup, "-C", tmpOld)
    
    if err := cmdNew.Run(); err != nil {
        return false, err
    }
    if err := cmdOld.Run(); err != nil {
        return false, err
    }

    // Сравниваем содержимое директорий
    cmd := exec.Command("diff", "-r", "--no-dereference", tmpNew, tmpOld)
    err := cmd.Run()
    
    if err == nil {
        return true, nil // Директории идентичны
    }
    if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
        return false, nil // Директории различаются
    }
    return false, err // Произошла ошибка
}

// runCommand runs a command on the remote server using a fresh session
func (sb *SSHBackup) runCommand(cmd string) error {
    session, err := sb.client.NewSession()
    if err != nil {
        return fmt.Errorf("failed to create session: %v", err)
    }
    defer session.Close()

    output, err := session.CombinedOutput(cmd)
    if err != nil {
        return fmt.Errorf("command failed: %v, output: %s", err, string(output))
    }
    return nil
}

// copyFileFromRemote copies a file from remote to local using scp
func (sb *SSHBackup) copyFileFromRemote(remotePath, localPath string) error {
    var cmd *exec.Cmd

    if sb.config.Password != "" {
        fmt.Printf("Using password authentication for SCP\n")
        cmd = exec.Command("/usr/bin/sshpass", "-p", sb.config.Password, "scp", 
            "-o", "StrictHostKeyChecking=no",
            "-P", sb.config.Port,
            fmt.Sprintf("%s@%s:%s", sb.config.User, sb.config.Host, remotePath),
            localPath)
    } else {
        fmt.Printf("Using key authentication for SCP\n")
        args := []string{
            "-o", "StrictHostKeyChecking=no",
            "-P", sb.config.Port,
        }
        if sb.config.KeyPath != "" {
            args = append(args, "-i", sb.config.KeyPath)
        }
        args = append(args, 
            fmt.Sprintf("%s@%s:%s", sb.config.User, sb.config.Host, remotePath),
            localPath)
        cmd = exec.Command("scp", args...)
    }

    fmt.Printf("Running SCP command: %v\n", cmd.Args)
    output, err := cmd.CombinedOutput()
    if err != nil {
        return fmt.Errorf("scp failed: %v, output: %s", err, string(output))
    }
    return nil
}

// backupRemoteFiles creates a backup of remote site files
func (sb *SSHBackup) backupRemoteFiles(site RemoteSite) error {
    timestamp := time.Now().Format("2006-01-02_150405")
    
    // Create remote temp directory structure similar to local
    remoteBaseDir := "~/laravel-backup-temp"
    remoteSiteDir := fmt.Sprintf("%s/%s", remoteBaseDir, site.ServerName)
    remoteBackupPath := fmt.Sprintf("%s/files_%s.tar.gz", remoteSiteDir, timestamp)
    
    // Ensure remote directories exist
    err := sb.runCommand(fmt.Sprintf("mkdir -p %s", remoteSiteDir))
    if err != nil {
        return fmt.Errorf("failed to create remote directory: %v", err)
    }

    // Create tar.gz archive on remote server (same as local version)
    cmd := fmt.Sprintf("cd %s && tar --exclude='./node_modules' -czf %s .", 
        site.DocumentRoot, remoteBackupPath)
    
    err = sb.runCommand(cmd)
    if err != nil {
        return fmt.Errorf("failed to create backup archive: %v", err)
    }

    // Prepare local directory
    localBackupDir := filepath.Join(sb.manager.BaseDir, site.ServerName)
    if err := os.MkdirAll(localBackupDir, 0755); err != nil {
        return fmt.Errorf("failed to create local directory: %v", err)
    }

    // Copy file from remote to local using scp
    localBackupPath := filepath.Join(localBackupDir, fmt.Sprintf("files_%s.tar.gz", timestamp))
    err = sb.copyFileFromRemote(remoteBackupPath, localBackupPath)
    if err != nil {
        return fmt.Errorf("failed to copy backup file: %v", err)
    }

    // Clean up remote backup file
    err = sb.runCommand(fmt.Sprintf("rm -f %s", remoteBackupPath))
    if err != nil {
        fmt.Printf("Warning: failed to remove remote backup file %s: %v\n", remoteBackupPath, err)
    }

    return sb.manager.cleanOldBackups(site.ServerName, false)
}

// backupRemoteDatabase creates a backup of remote site database
func (sb *SSHBackup) backupRemoteDatabase(site RemoteSite, dbHost, dbName, dbUser, dbPass string) error {
    timestamp := time.Now().Format("2006-01-02_150405")
    
    // Create remote temp directory structure similar to local
    remoteBaseDir := "~/laravel-backup-temp"
    remoteSiteDir := fmt.Sprintf("%s/%s/database", remoteBaseDir, site.ServerName)
    remoteBackupPath := fmt.Sprintf("%s/db_%s.sql.gz", remoteSiteDir, timestamp)
    
    // Ensure remote directories exist
    err := sb.runCommand(fmt.Sprintf("mkdir -p %s", remoteSiteDir))
    if err != nil {
        return fmt.Errorf("failed to create remote directory: %v", err)
    }

    // Create database backup on remote server (same as local version)
    cmd := fmt.Sprintf("mysqldump -h%s -u%s -p%s --quick --lock-tables=false %s | gzip > %s",
        dbHost, dbUser, dbPass, dbName, remoteBackupPath)
    
    err = sb.runCommand(cmd)
    if err != nil {
        return fmt.Errorf("failed to create database backup: %v", err)
    }

    // Prepare local directory
    localBackupDir := filepath.Join(sb.manager.BaseDir, site.ServerName, "database")
    if err := os.MkdirAll(localBackupDir, 0755); err != nil {
        return fmt.Errorf("failed to create local directory: %v", err)
    }

    // Copy file from remote to local using scp
    localBackupPath := filepath.Join(localBackupDir, fmt.Sprintf("db_%s.sql.gz", timestamp))
    err = sb.copyFileFromRemote(remoteBackupPath, localBackupPath)
    if err != nil {
        return fmt.Errorf("failed to copy backup file: %v", err)
    }

    // Clean up remote backup file
    err = sb.runCommand(fmt.Sprintf("rm -f %s", remoteBackupPath))
    if err != nil {
        fmt.Printf("Warning: failed to remove remote backup file %s: %v\n", remoteBackupPath, err)
    }

    return sb.manager.cleanOldBackups(site.ServerName, true)
}

// backupSite backs up a single site
func (sb *SSHBackup) backupSite(site SiteInfo) error {
    // Convert SiteInfo to RemoteSite for compatibility with existing code
    remoteSite := RemoteSite{
        ServerName:   site.ServerName,
        DocumentRoot: site.DocumentRoot,
    }

    if err := sb.backupRemoteFiles(remoteSite); err != nil {
        return fmt.Errorf("failed to backup files: %v", err)
    }

    if site.DBName != "" && site.DBUser != "" {
        if err := sb.backupRemoteDatabase(remoteSite, site.DBHost, site.DBName, site.DBUser, site.DBPass); err != nil {
            return fmt.Errorf("failed to backup database: %v", err)
        }
    }

    return nil
}
