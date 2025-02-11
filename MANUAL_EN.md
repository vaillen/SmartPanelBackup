# Laravel Backup Tool User Manual

## General Information

Laravel Backup Tool is an automated backup solution for Laravel sites that performs:
1. Site file backups
2. Database backups
3. Automatic rotation of old backups
4. Daily change detection

## File Locations

### On the sites server (remote server):
- Temporary backup directory: `~/laravel-backup-temp/`
- Sites are located in their standard directories (e.g., `/home/user/public_html/`)

### On the backup server (local server):
- Main backup directory: `/laravel-backup-script/`
- Each site gets its own subdirectory named after the domain
- Backup structure:
  ```
  /laravel-backup-script/
  ├── example.com/
  │   ├── files_2025-02-11_130000.tar.gz
  │   └── db_2025-02-11_130000.sql.gz
  ├── another-site.com/
  │   ├── files_2025-02-11_130000.tar.gz
  │   └── db_2025-02-11_130000.sql.gz
  ```

## How It Works

1. **Execution Time**
   - The script runs automatically every day at 13:00 Kyiv time (11:00 UTC)
   - Logs are written to `/var/log/laravel-backup.log`

2. **Backup Process**
   - The script checks each site for changes in the last 24 hours
   - If changes are found or there's no backup for today:
     * Creates a site file archive (excluding node_modules)
     * Creates a database dump
     * Files are copied to the backup server
   - If no changes and today's backup exists - the site is skipped

3. **Backup Rotation**
   - Old backups are automatically deleted according to .env settings
   - By default, keeps the last 7 file backups and 7 database backups

## Accessing Backups

1. **SSH Access**
   ```bash
   ssh user@backup-server
   cd /laravel-backup-script
   ```

2. **Backup Structure**
   - Files: `files_DATE_TIME.tar.gz`
   - Databases: `db_DATE_TIME.sql.gz`

3. **Restoring from Backup**
   - Files:
     ```bash
     tar -xzf files_DATE_TIME.tar.gz
     ```
   - Database:
     ```bash
     gunzip < db_DATE_TIME.sql.gz | mysql -u USER -p DATABASE
     ```

## Monitoring

1. **Logs**
   - All actions are logged to `/var/log/laravel-backup.log`
   - Log format includes:
     * Operation time
     * Site name
     * Operation status
     * Errors (if any)

2. **Status Check**
   ```bash
   tail -f /var/log/laravel-backup.log
   ```

## Security

- All connections are made via SSH using keys
- Backups are stored in a directory accessible only to root
- Database passwords are not stored but taken from site .env files

## Setting Up New Sites

1. Ensure the site has a correct .env file with database settings
2. The site will be automatically discovered and added to the backup list
