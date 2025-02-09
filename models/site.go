package models

// Site represents a Laravel website configuration
type Site struct {
    // ServerName from Apache configuration
    ServerName    string
    // DocumentRoot from Apache configuration
    DocumentRoot  string
    // Database connection details from Laravel .env
    DatabaseHost  string
    DatabaseName  string
    DatabaseUser  string
    DatabasePass  string
}
