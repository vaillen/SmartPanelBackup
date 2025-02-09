package backup

import (
    "fmt"
    "os"
    "path/filepath"
    "strings"
    "golang.org/x/crypto/ssh"
    "io/ioutil"
    "time"
    "sync"
)

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
}

// NewSSHBackup creates a new SSH backup handler
func NewSSHBackup(config *SSHConfig) (*SSHBackup, error) {
    var authMethods []ssh.AuthMethod

    // Add private key authentication if key path is provided
    if config.KeyPath != "" {
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

    // Add password authentication if password is provided
    if config.Password != "" {
        authMethods = append(authMethods, ssh.Password(config.Password))
    }

    sshConfig := &ssh.ClientConfig{
        User: config.User,
        Auth: authMethods,
        HostKeyCallback: ssh.InsecureIgnoreHostKey(), // Note: In production, use proper host key verification
        Timeout: 30 * time.Second,
    }

    // Connect to remote server
    client, err := ssh.Dial("tcp", fmt.Sprintf("%s:%s", config.Host, config.Port), sshConfig)
    if err != nil {
        return nil, fmt.Errorf("unable to connect to SSH server: %v", err)
    }

    // Initialize backup manager for SSH backups
    manager, err := NewBackupManager("/laravel-backup-script-ssh")
    if err != nil {
        client.Close()
        return nil, fmt.Errorf("failed to initialize backup manager: %v", err)
    }

    return &SSHBackup{
        config:  config,
        client:  client,
        manager: manager,
    }, nil
}

// Close closes the SSH connection
func (sb *SSHBackup) Close() error {
    if sb.client != nil {
        return sb.client.Close()
    }
    return nil
}

// getRemoteSites gets the list of Laravel sites from remote Apache configuration
func (sb *SSHBackup) getRemoteSites() ([]RemoteSite, error) {
    session, err := sb.client.NewSession()
    if err != nil {
        return nil, fmt.Errorf("failed to create session: %v", err)
    }
    defer session.Close()

    // Read Apache configuration
    cmd := `cat /etc/apache2/conf/httpd.conf`
    output, err := session.Output(cmd)
    if err != nil {
        return nil, fmt.Errorf("failed to read Apache config: %v", err)
    }

    // Parse Apache configuration using the same logic as local
    sites := make(map[string]string)
    lines := strings.Split(string(output), "\n")
    var serverName, documentRoot string

    for _, line := range lines {
        line = strings.TrimSpace(line)
        if strings.HasPrefix(strings.ToLower(line), "servername") {
            serverName = strings.Fields(line)[1]
        } else if strings.HasPrefix(strings.ToLower(line), "documentroot") {
            documentRoot = strings.Trim(strings.Fields(line)[1], "\"")
            if serverName != "" {
                sites[serverName] = documentRoot
                serverName = ""
            }
        }
    }

    // Get .env content for each site
    var remoteSites []RemoteSite
    for serverName, docRoot := range sites {
        site := RemoteSite{
            ServerName:   serverName,
            DocumentRoot: docRoot,
        }

        // Try to read .env file
        envSession, err := sb.client.NewSession()
        if err != nil {
            continue
        }
        envContent, _ := envSession.Output(fmt.Sprintf("cat %s/.env", docRoot))
        envSession.Close()
        site.EnvContent = string(envContent)

        remoteSites = append(remoteSites, site)
    }

    return remoteSites, nil
}

// BackupRemoteSites performs backup of all sites on the remote server
func (sb *SSHBackup) BackupRemoteSites() error {
    // Get remote sites
    sites, err := sb.getRemoteSites()
    if err != nil {
        return fmt.Errorf("failed to get remote sites: %v", err)
    }

    // Channel for collecting backup results
    resultChan := make(chan struct {
        site RemoteSite
        err  error
    })

    // WaitGroup to track all running goroutines
    var wg sync.WaitGroup

    // Process each site in parallel
    for _, site := range sites {
        wg.Add(1)
        go func(site RemoteSite) {
            defer wg.Done()
            err := sb.backupRemoteSite(site)
            resultChan <- struct {
                site RemoteSite
                err  error
            }{site, err}
        }(site)
    }

    // Start a goroutine to close result channel when all backups are done
    go func() {
        wg.Wait()
        close(resultChan)
    }()

    // Collect results
    var errors []string
    for result := range resultChan {
        if result.err != nil {
            errors = append(errors, fmt.Sprintf("Failed to backup %s: %v", result.site.ServerName, result.err))
        } else {
            fmt.Printf("Successfully backed up remote site %s\n", result.site.ServerName)
        }
    }

    if len(errors) > 0 {
        return fmt.Errorf("backup errors occurred:\n%s", strings.Join(errors, "\n"))
    }

    return nil
}

// backupRemoteSite backs up a single remote site
func (sb *SSHBackup) backupRemoteSite(site RemoteSite) error {
    // Create backup directory
    siteBackupDir := filepath.Join(sb.manager.BaseDir, site.ServerName)
    if err := os.MkdirAll(siteBackupDir, 0755); err != nil {
        return fmt.Errorf("failed to create backup directory: %v", err)
    }

    // Backup files
    if err := sb.backupRemoteFiles(site); err != nil {
        return fmt.Errorf("failed to backup files: %v", err)
    }

    // Parse .env content for database credentials
    if site.EnvContent != "" {
        dbHost, dbName, dbUser, dbPass := parseEnvContent(site.EnvContent)
        if dbHost != "" && dbName != "" && dbUser != "" && dbPass != "" {
            if err := sb.backupRemoteDatabase(site, dbHost, dbName, dbUser, dbPass); err != nil {
                return fmt.Errorf("failed to backup database: %v", err)
            }
        }
    }

    return nil
}

// backupRemoteFiles creates a backup of remote site files
func (sb *SSHBackup) backupRemoteFiles(site RemoteSite) error {
    session, err := sb.client.NewSession()
    if err != nil {
        return fmt.Errorf("failed to create session: %v", err)
    }
    defer session.Close()

    timestamp := time.Now().Format("2006-01-02_150405")
    backupPath := filepath.Join(sb.manager.BaseDir, site.ServerName, fmt.Sprintf("files_%s.tar.gz", timestamp))

    // Create tar.gz on remote server and stream it directly to local file
    // Exclude node_modules and use -h for symlinks
    cmd := fmt.Sprintf("cd %s && tar czfh - --exclude='node_modules' .", site.DocumentRoot)
    
    // Create local file
    localFile, err := os.Create(backupPath)
    if err != nil {
        return fmt.Errorf("failed to create local file: %v", err)
    }
    defer localFile.Close()

    session.Stdout = localFile
    if err := session.Run(cmd); err != nil {
        return fmt.Errorf("failed to create backup: %v", err)
    }

    // Clean old backups
    return sb.manager.cleanOldBackups(site.ServerName, false)
}

// backupRemoteDatabase creates a backup of remote site database
func (sb *SSHBackup) backupRemoteDatabase(site RemoteSite, dbHost, dbName, dbUser, dbPass string) error {
    session, err := sb.client.NewSession()
    if err != nil {
        return fmt.Errorf("failed to create session: %v", err)
    }
    defer session.Close()

    timestamp := time.Now().Format("2006-01-02_150405")
    dbBackupDir := filepath.Join(sb.manager.BaseDir, site.ServerName, "database")
    if err := os.MkdirAll(dbBackupDir, 0755); err != nil {
        return fmt.Errorf("failed to create database backup directory: %v", err)
    }

    backupPath := filepath.Join(dbBackupDir, fmt.Sprintf("db_%s.sql.gz", timestamp))

    // Create mysqldump command without --single-transaction
    cmd := fmt.Sprintf("mysqldump -h%s -u%s -p%s --quick --lock-tables=false %s | gzip",
        dbHost, dbUser, dbPass, dbName)

    // Create local file
    localFile, err := os.Create(backupPath)
    if err != nil {
        return fmt.Errorf("failed to create local file: %v", err)
    }
    defer localFile.Close()

    session.Stdout = localFile
    if err := session.Run(cmd); err != nil {
        return fmt.Errorf("failed to create database backup: %v", err)
    }

    // Clean old backups
    return sb.manager.cleanOldBackups(site.ServerName, true)
}

// parseEnvContent parses database credentials from .env content
func parseEnvContent(content string) (host, name, user, pass string) {
    lines := strings.Split(content, "\n")
    for _, line := range lines {
        line = strings.TrimSpace(line)
        if line == "" || strings.HasPrefix(line, "#") {
            continue
        }

        parts := strings.SplitN(line, "=", 2)
        if len(parts) != 2 {
            continue
        }

        key := strings.TrimSpace(parts[0])
        value := strings.TrimSpace(parts[1])
        value = strings.Trim(value, "\"'")

        switch key {
        case "DB_HOST":
            host = value
        case "DB_DATABASE":
            name = value
        case "DB_USERNAME":
            user = value
        case "DB_PASSWORD":
            pass = value
        }
    }
    return
}
