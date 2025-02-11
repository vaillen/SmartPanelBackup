# Laravel Backup Tool

A powerful and flexible backup solution for Laravel applications, supporting both local and remote backups with intelligent file comparison and rotation.

## Features

- **Local Backups**: Backup Laravel applications on the local machine
- **Remote Backups**: Backup Laravel applications from remote servers via SSH
- **Intelligent Backup**: Compares backups to avoid duplicates
- **Database Support**: Automatically detects and backs up MySQL databases
- **Backup Rotation**: Maintains a configurable number of backups
- **Parallel Processing**: Uses concurrent processing for local backups
- **Sequential Processing**: Uses safe sequential processing for remote backups
- **Configurable**: Easily customizable through environment variables

## Requirements

- Go 1.21 or higher
- `sshpass` (for password-based SSH authentication)
- `mysqldump` (for database backups)
- `tar` and `gzip` (for file compression)

## Installation

1. Clone the repository:
```bash
git clone https://github.com/yourusername/laravel-backup-tool.git
cd laravel-backup-tool
```

2. Build the application:
```bash
go build
```

3. Copy the example environment file:
```bash
cp .env.example .env
```

4. Configure your environment variables in `.env`

## Configuration

### Environment Variables

#### General Settings
- `BACKUP_DIR`: Directory for local backups (default: `/laravel-backup-script`)
- `REMOTE_BACKUP_DIR`: Directory for remote backups (default: `/laravel-backup-script-ssh`)
- `REMOTE_BACKUP_ENABLED`: Enable/disable remote backups (true/false)

#### Local Backup Settings
- `LOCAL_MAX_FILE_BACKUPS`: Maximum number of file backups to keep (default: 5)
- `LOCAL_MAX_DB_BACKUPS`: Maximum number of database backups to keep (default: 20)

#### Remote Backup Settings
- `REMOTE_MAX_FILE_BACKUPS`: Maximum number of remote file backups to keep (default: 5)
- `REMOTE_MAX_DB_BACKUPS`: Maximum number of remote database backups to keep (default: 20)
- `SSH_HOST`: Remote server hostname or IP
- `SSH_PORT`: SSH port (default: 22)
- `SSH_USER`: SSH username
- `SSH_PASSWORD`: SSH password (if using password authentication)
- `SSH_KEY_PATH`: Path to SSH private key (if using key authentication)

## Usage

### Basic Usage

Run the backup tool:
```bash
./laravel-backup-tool
```

### Backup Process

#### Local Backups
1. Scans Apache configuration to find Laravel sites
2. For each site:
   - Creates a tar.gz archive of site files (excluding node_modules)
   - Reads .env file for database credentials
   - Creates MySQL database dump if credentials found
   - Compares new backup with previous backup
   - Removes duplicate backups
   - Rotates old backups based on configuration

#### Remote Backups
1. Connects to remote server via SSH
2. Scans Apache configuration to find Laravel sites
3. For each site:
   - Creates temporary directory
   - Archives site files on remote server
   - Reads .env file for database credentials
   - Creates MySQL database dump on remote server
   - Copies files to local machine via SCP
   - Compares with previous backup
   - Removes duplicate backups
   - Rotates old backups based on configuration
   - Cleans up temporary files

### Backup Directory Structure

```
backup-directory/
├── site1.example.com/
│   ├── files_2025-02-10_220130.tar.gz
│   ├── files_2025-02-09_220130.tar.gz
│   ├── db_2025-02-10_220130.sql.gz
│   └── db_2025-02-09_220130.sql.gz
└── site2.example.com/
    ├── files_2025-02-10_220130.tar.gz
    └── db_2025-02-10_220130.sql.gz
```

### Backup Rotation

The tool maintains a limited number of backups:
- Keeps the most recent backups based on `MAX_FILE_BACKUPS` and `MAX_DB_BACKUPS`
- Automatically removes older backups
- Different limits can be set for local and remote backups

## Error Handling

- All errors are logged with detailed messages
- Backup process continues even if one site fails
- Failed backups don't affect other site backups
- Temporary files are cleaned up even after errors

## Security

- Supports both password and key-based SSH authentication
- Database credentials are read from .env files
- Temporary files are securely cleaned up
- No sensitive information in error logs

## Best Practices

1. **SSH Authentication**:
   - Prefer key-based authentication over passwords
   - Use a dedicated backup user with limited permissions

2. **Backup Rotation**:
   - Keep more database backups than file backups
   - Adjust rotation settings based on storage capacity

3. **Monitoring**:
   - Regularly check backup logs
   - Verify backup integrity
   - Monitor disk space usage

4. **Performance**:
   - Schedule backups during low-traffic periods
   - Use appropriate MAX_BACKUPS settings
   - Monitor backup duration and size

## Troubleshooting

### Common Issues

1. SSH Connection Failures:
   - Verify SSH credentials
   - Check SSH port and firewall settings
   - Ensure sshpass is installed for password auth

2. Database Backup Failures:
   - Verify database credentials in .env
   - Check MySQL server connectivity
   - Ensure mysqldump is installed

3. Permission Issues:
   - Check backup directory permissions
   - Verify SSH user permissions
   - Check MySQL user privileges

### Debug Mode

Add verbose logging by setting:
```bash
export DEBUG=true
```

## Contributing

1. Fork the repository
2. Create your feature branch
3. Commit your changes
4. Push to the branch
5. Create a Pull Request

## License

This project is licensed under the MIT License - see the LICENSE file for details.