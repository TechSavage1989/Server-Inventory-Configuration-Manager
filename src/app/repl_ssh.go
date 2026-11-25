package main

// Installing the Linux agent from the console over SSH. The host key must
// match a fingerprint the operator trusted (shown on first contact, as with
// vCenter certificates); the password is used for the connection and for
// sudo (sent on standard input, never on a command line) and never saved
// or logged. The agent is uploaded to a private temporary folder, installed
// with --agent-install as root, and the folder removed.

import (
	"bytes"
	"context"
	"debug/elf"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// replLinuxAgentName is the Linux agent's release asset and the file the
// console looks for beside its own executable.
const replLinuxAgentName = "simm-agent-linux-amd64"

type replSSHTarget struct {
	Host       string
	Port       int
	User       string
	Password   string
	PrivateKey []byte
	// HostKey is the SHA256 fingerprint the operator trusted.
	HostKey string
}

// replUntrustedHostKey is returned when the server's key is not the one
// trusted; the operator can trust Fingerprint and try again.
type replUntrustedHostKey struct {
	Host        string
	Fingerprint string
	Changed     bool
}

func (e *replUntrustedHostKey) Error() string {
	if e.Changed {
		return fmt.Sprintf("the SSH host key of %s is %s, not the one trusted before; check the server before trusting it", e.Host, e.Fingerprint)
	}
	return fmt.Sprintf("the SSH host key of %s (%s) is not trusted yet", e.Host, e.Fingerprint)
}

var (
	replSSHTempDir    = regexp.MustCompile(`^/tmp/simm-agent\.[A-Za-z0-9]{6,12}$`)
	replSSHKeyLine    = regexp.MustCompile(`Agent key:\s+([a-z2-7]{32})`)
	replSSHThumbLine  = regexp.MustCompile(`Certificate thumbprint:\s+([0-9A-F:]{95})`)
	replSSHUserPatten = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.@-]{0,63}$`)
)

func replSSHDial(ctx context.Context, target replSSHTarget) (*ssh.Client, error) {
	if !replSSHUserPatten.MatchString(target.User) {
		return nil, errors.New("type the Linux account name")
	}
	methods := []ssh.AuthMethod{}
	if len(target.PrivateKey) > 0 {
		signer, err := ssh.ParsePrivateKey(target.PrivateKey)
		if err != nil {
			return nil, errors.New("the private key could not be read (keys with a passphrase are not supported)")
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if target.Password != "" {
		password := target.Password
		methods = append(methods, ssh.Password(password), ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
			answers := make([]string, len(questions))
			for i := range answers {
				answers[i] = password
			}
			return answers, nil
		}))
	}
	if len(methods) == 0 {
		return nil, errors.New("type the account's password or choose its private key")
	}
	port := target.Port
	if port == 0 {
		port = 22
	}
	address := net.JoinHostPort(target.Host, strconv.Itoa(port))
	config := &ssh.ClientConfig{
		User: target.User,
		Auth: methods,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			fingerprint := ssh.FingerprintSHA256(key)
			if target.HostKey != fingerprint {
				return &replUntrustedHostKey{Host: target.Host, Fingerprint: fingerprint, Changed: target.HostKey != ""}
			}
			return nil
		},
		Timeout: 20 * time.Second,
	}
	dialer := net.Dialer{Timeout: 20 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("%s could not be reached on SSH port %d: %w", target.Host, port, err)
	}
	connection, channels, requests, err := ssh.NewClientConn(conn, address, config)
	if err != nil {
		conn.Close()
		var untrusted *replUntrustedHostKey
		if errors.As(err, &untrusted) {
			return nil, untrusted
		}
		if strings.Contains(err.Error(), "unable to authenticate") {
			return nil, fmt.Errorf("%s refused the account or password", target.Host)
		}
		return nil, err
	}
	return ssh.NewClient(connection, channels, requests), nil
}

// replSSHRun runs one command and returns its combined output (at most
// limit bytes kept).
func replSSHRun(ctx context.Context, client *ssh.Client, command string, stdin io.Reader) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	var out replLimitedBuffer
	out.limit = 64 << 10
	session.Stdout, session.Stderr, session.Stdin = &out, &out, stdin
	done := make(chan error, 1)
	go func() { done <- session.Run(command) }()
	select {
	case err = <-done:
	case <-ctx.Done():
		session.Signal(ssh.SIGTERM)
		return out.String(), ctx.Err()
	}
	return strings.TrimSpace(out.String()), err
}

// replLimitedBuffer keeps the first limit bytes; standard output and
// error are copied into it by separate goroutines, hence the lock.
type replLimitedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	limit  int
}

func (b *replLimitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func (b *replLimitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := b.limit - b.buffer.Len(); room > 0 {
		if len(p) > room {
			b.buffer.Write(p[:room])
		} else {
			b.buffer.Write(p)
		}
	}
	return len(p), nil
}

// replSSHSudo prefixes a command with sudo reading the password from
// standard input, unless the account is root.
func replSSHSudo(target replSSHTarget, command string) (string, io.Reader) {
	if target.User == "root" {
		return command, strings.NewReader("")
	}
	return "sudo -S -p '' " + command, strings.NewReader(target.Password + "\n")
}

// replSSHScrub removes the password from text that may be shown.
func replSSHScrub(text, password string) string {
	if password != "" {
		text = strings.ReplaceAll(text, password, "********")
	}
	return text
}

// replLoadLinuxAgent reads the Linux agent and checks it is a 64-bit x86
// Linux program.
func replLoadLinuxAgent(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("the Linux agent %s was not found; download %s from the release beside this program", path, replLinuxAgentName)
	}
	if info.Size() > 128<<20 {
		return nil, errors.New("the Linux agent file is too large")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	file, err := elf.NewFile(bytes.NewReader(raw))
	if err != nil || file.Machine != elf.EM_X86_64 || file.Class != elf.ELFCLASS64 || file.Type != elf.ET_EXEC {
		return nil, fmt.Errorf("%s is not the 64-bit Linux agent", path)
	}
	return raw, nil
}

// replSSHInstallAgent uploads and installs the agent and returns the key
// and certificate thumbprint it printed.
func replSSHInstallAgent(ctx context.Context, target replSSHTarget, agent []byte, port int, log func(string)) (key, thumbprint string, err error) {
	client, err := replSSHDial(ctx, target)
	if err != nil {
		return "", "", err
	}
	defer client.Close()
	log(fmt.Sprintf("%s: copying the agent over SSH.", target.Host))
	dir, err := replSSHRun(ctx, client, `umask 077 && d=$(mktemp -d /tmp/simm-agent.XXXXXXXX) && cat > "$d/simm-agent" && chmod 700 "$d/simm-agent" && printf '%s' "$d"`, bytes.NewReader(agent))
	if err != nil || !replSSHTempDir.MatchString(dir) {
		return "", "", fmt.Errorf("the agent could not be copied to %s: %s", target.Host, replSSHScrub(strings.TrimSpace(dir+" "+fmt.Sprint(err)), target.Password))
	}
	defer replSSHRun(context.Background(), client, "rm -rf '"+dir+"'", nil)
	log(fmt.Sprintf("%s: installing the agent service.", target.Host))
	command, stdin := replSSHSudo(target, "'"+dir+"/simm-agent' --agent-install --port "+strconv.Itoa(port))
	out, err := replSSHRun(ctx, client, command, stdin)
	out = replSSHScrub(out, target.Password)
	if err != nil {
		if strings.Contains(out, "a terminal is required") || strings.Contains(out, "no tty present") {
			return "", "", fmt.Errorf("sudo on %s needs a terminal; allow this account to use sudo without one, or use root", target.Host)
		}
		if strings.Contains(out, "incorrect password") || strings.Contains(out, "is not in the sudoers") || strings.Contains(out, "not allowed to") {
			return "", "", fmt.Errorf("%s did not let this account use sudo: %s", target.Host, out)
		}
		return "", "", fmt.Errorf("the install on %s failed: %s", target.Host, out)
	}
	keyMatch, thumbMatch := replSSHKeyLine.FindStringSubmatch(out), replSSHThumbLine.FindStringSubmatch(out)
	if keyMatch == nil || thumbMatch == nil {
		return "", "", fmt.Errorf("the agent on %s did not report its key and thumbprint: %s", target.Host, out)
	}
	return keyMatch[1], thumbMatch[1], nil
}

// replSSHUninstallAgent removes the agent from a Linux server.
func replSSHUninstallAgent(ctx context.Context, target replSSHTarget) error {
	client, err := replSSHDial(ctx, target)
	if err != nil {
		return err
	}
	defer client.Close()
	command, stdin := replSSHSudo(target, "/usr/local/sbin/simm-agent --agent-uninstall")
	if out, err := replSSHRun(ctx, client, command, stdin); err != nil {
		return fmt.Errorf("%s: %s", err, replSSHScrub(out, target.Password))
	}
	return nil
}
