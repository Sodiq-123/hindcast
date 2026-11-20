// Package mail sends plain-text emails (password resets) over SMTP.
package mail

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

type Sender struct {
	Host, Port, User, Pass, From string
}

// New returns nil when SMTP isn't configured; Enabled is nil-safe.
func New(host, port, user, pass, from string) *Sender {
	if host == "" || from == "" {
		return nil
	}
	return &Sender{Host: host, Port: port, User: user, Pass: pass, From: from}
}

func (s *Sender) Enabled() bool { return s != nil }

// Send delivers one message. Port 465 uses TLS from the start; other ports
// upgrade with STARTTLS, which is required before sending a password.
func (s *Sender) Send(to, subject, body string) error {
	if s == nil {
		return fmt.Errorf("email is not configured (set SMTP_HOST and SMTP_FROM)")
	}
	for _, v := range []string{to, subject} {
		if strings.ContainsAny(v, "\r\n") {
			return fmt.Errorf("invalid header value")
		}
	}
	addr := net.JoinHostPort(s.Host, s.Port)
	var c *smtp.Client
	var err error
	if s.Port == "465" {
		conn, derr := tls.DialWithDialer(&net.Dialer{Timeout: 15 * time.Second}, "tcp", addr, &tls.Config{ServerName: s.Host})
		if derr != nil {
			return derr
		}
		c, err = smtp.NewClient(conn, s.Host)
	} else {
		conn, derr := net.DialTimeout("tcp", addr, 15*time.Second)
		if derr != nil {
			return derr
		}
		if c, err = smtp.NewClient(conn, s.Host); err == nil {
			if ok, _ := c.Extension("STARTTLS"); ok {
				err = c.StartTLS(&tls.Config{ServerName: s.Host})
			} else if s.User != "" {
				err = fmt.Errorf("SMTP server offers no STARTTLS; refusing to send the password unencrypted")
			}
		}
	}
	if err != nil {
		if c != nil {
			c.Close()
		}
		return err
	}
	defer c.Close()
	if s.User != "" {
		if err := c.Auth(smtp.PlainAuth("", s.User, s.Pass, s.Host)); err != nil {
			return err
		}
	}
	if err := c.Mail(s.From); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	msg := "From: " + s.From + "\r\nTo: " + to + "\r\nSubject: " + subject +
		"\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nDate: " + time.Now().Format(time.RFC1123Z) +
		"\r\n\r\n" + strings.ReplaceAll(body, "\n", "\r\n")
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}
