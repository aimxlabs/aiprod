package config

import (
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	DataDir    string
	Domain     string
	HTTPAddr   string
	SMTPAddr   string
	IMAPAddr   string
	NoAuth     bool
	AdminKey   string
	TLSCert   string
	TLSKey    string
}

func Default() *Config {
	dataDir := os.Getenv("AIPROD_DATA_DIR")
	if dataDir == "" {
		dataDir = "./data"
	}
	domain := os.Getenv("AIPROD_DOMAIN")
	if domain == "" {
		domain = "localhost"
	}
	httpAddr := os.Getenv("AIPROD_HTTP_ADDR")
	if httpAddr == "" {
		httpAddr = ":8600"
	}
	smtpAddr := os.Getenv("AIPROD_SMTP_ADDR")
	if smtpAddr == "" {
		smtpAddr = ":2525"
	}
	imapAddr := os.Getenv("AIPROD_IMAP_ADDR")
	if imapAddr == "" {
		imapAddr = ":1993"
	}

	return &Config{
		DataDir:  dataDir,
		Domain:   domain,
		HTTPAddr: httpAddr,
		SMTPAddr: smtpAddr,
		IMAPAddr: imapAddr,
		NoAuth:   os.Getenv("AIPROD_NO_AUTH") == "1",
		AdminKey: os.Getenv("AIPROD_ADMIN_KEY"),
		TLSCert:  os.Getenv("AIPROD_TLS_CERT"),
		TLSKey:   os.Getenv("AIPROD_TLS_KEY"),
	}
}

func (c *Config) CoreDBPath() string {
	return filepath.Join(c.DataDir, "core.db")
}

func (c *Config) EmailDBPath() string {
	return filepath.Join(c.DataDir, "email.db")
}

func (c *Config) TablesDBPath() string {
	return filepath.Join(c.DataDir, "tables.db")
}

func (c *Config) ObserveDBPath() string {
	return filepath.Join(c.DataDir, "observe.db")
}

func (c *Config) EmailRawDir() string {
	return filepath.Join(c.DataDir, "email", "raw")
}

func (c *Config) DocsDir() string {
	return filepath.Join(c.DataDir, "docs")
}

func (c *Config) FilesDir() string {
	return filepath.Join(c.DataDir, "files")
}

func (c *Config) EnsureDirectories() error {
	dirs := []string{
		c.DataDir,
		c.EmailRawDir(),
		c.DocsDir(),
		c.FilesDir(),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return fmt.Errorf("creating directory %s: %w", dir, err)
		}
	}
	return nil
}
