package biz

import (
	"context"
	"github.com/stretchr/testify/require"
	"net"
	"strconv"
	"testing"
	"time"
)

func TestSMTPHeaderInjectionRejected(t *testing.T) {
	s := NewMultiSender(SenderConfig{SMTPHost: "invalid.example", SMTPFrom: "sender@example.com"})
	for _, n := range []*Notification{{Type: NotifyTypeEmail, Recipient: "a@example.com", Subject: "hello\r\nBcc:evil@example.com"}, {Type: NotifyTypeEmail, Recipient: "a@example.com\r\nBcc:evil@example.com", Subject: "hello"}} {
		require.ErrorContains(t, s.Send(context.Background(), n), "invalid email header")
	}
}
func TestWebhookRejectsPrivateDestination(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "")
	err := NewMultiSender(SenderConfig{}).Send(context.Background(), &Notification{Type: NotifyTypeWebhook, Recipient: "http://127.0.0.1:1/private"})
	require.ErrorContains(t, err, "private/reserved")
}
func TestNotificationUnknownTypeRejected(t *testing.T) {
	_, err := NewNotifyUsecase(&mockNotifyRepo{entries: make(map[int64]*Notification)}).CreateNotification(context.Background(), "typo", "", "", "test")
	require.ErrorIs(t, err, ErrUnsupportedNotificationType)
}

func TestSMTPCancelClosesConnection(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	number, _ := strconv.Atoi(port)
	sender := NewMultiSender(SenderConfig{SMTPHost: host, SMTPPort: number, SMTPFrom: "sender@example.com"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		finished <- sender.Send(ctx, &Notification{Type: NotifyTypeEmail, Recipient: "to@example.com", Subject: "test"})
	}()
	var conn net.Conn
	select {
	case conn = <-accepted:
	case <-time.After(time.Second):
		t.Fatal("SMTP did not connect")
	}
	defer conn.Close()
	cancel()
	select {
	case err := <-finished:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("SMTP cancellation leaked connection")
	}
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
	_, err = conn.Read(make([]byte, 1))
	require.Error(t, err)
}
