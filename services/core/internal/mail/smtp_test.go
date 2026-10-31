package mail

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// relay answers like an SMTP server that offers no STARTTLS, or, with
// silent, accepts the connection and never says anything.
func relay(t *testing.T, silent bool) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				if silent {
					time.Sleep(5 * time.Second)
					return
				}
				r := bufio.NewReader(c)
				c.Write([]byte("220 relay ready\r\n"))
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					switch {
					case strings.HasPrefix(line, "EHLO"):
						c.Write([]byte("250-relay\r\n250 8BITMIME\r\n"))
					case strings.HasPrefix(line, "QUIT"):
						c.Write([]byte("221 bye\r\n"))
						return
					default:
						c.Write([]byte("250 ok\r\n"))
					}
				}
			}()
		}
	}()
	return ln.Addr().String()
}

func TestSMTPRefusesARelayWithoutSTARTTLS(t *testing.T) {
	s := SMTP{Addr: relay(t, false), From: "AssessMatrix <noreply@example.id>"}
	err := s.Send(context.Background(), Message{ID: "1", To: "a@example.id", Subject: "Hi", Body: "x"})
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("sent without TLS: %v", err)
	}
}

func TestSMTPGivesUpOnASilentRelay(t *testing.T) {
	s := SMTP{Addr: relay(t, true), From: "noreply@example.id"}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := s.Send(ctx, Message{ID: "1", To: "a@example.id", Subject: "Hi", Body: "x"}); err == nil {
		t.Fatal("a relay that never answered accepted the message")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("waited %s on a silent relay", d)
	}
}

func TestSMTPRefusesABadSender(t *testing.T) {
	s := SMTP{Addr: "127.0.0.1:1", From: "not an address"}
	if err := s.Send(context.Background(), Message{ID: "1", To: "a@example.id"}); err == nil {
		t.Fatal("a malformed AM_SMTP_FROM was used")
	}
}
