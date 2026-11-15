package agents

// No model, credentials, production hosts or real SSH keys are used. Run on
// a host that permits Codex to launch its OS sandbox:
// EGGIFY_CODEX_SANDBOX_PROBE=1 go test ./internal/server/agents -run TestCodexNetworkOS -v

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Render inline TOML tables for CLI overrides. Splitting filesystem paths into
// dotted -c keys is incorrect: the CLI key parser treats quotes literally.
func codexProbeArgs(cfg map[string]any) []string {
	var value func(any) string
	value = func(v any) string {
		switch v := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(v))
			for k := range v {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			entries := make([]string, 0, len(keys))
			for _, k := range keys {
				entries = append(entries, strconv.Quote(k)+"="+value(v[k]))
			}
			return "{" + strings.Join(entries, ",") + "}"
		case map[string]string:
			m := map[string]any{}
			for k, s := range v {
				m[k] = s
			}
			return value(m)
		case []string:
			entries := make([]string, len(v))
			for i, s := range v {
				entries[i] = strconv.Quote(s)
			}
			return "[" + strings.Join(entries, ",") + "]"
		case string:
			return strconv.Quote(v)
		case bool:
			return strconv.FormatBool(v)
		default:
			panic(fmt.Sprintf("unsupported probe config %T", v))
		}
	}
	keys := make([]string, 0, len(cfg))
	for k := range cfg {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var args []string
	for _, k := range keys {
		args = append(args, "-c", k+"="+value(cfg[k]))
	}
	return args
}

func TestCodexNetworkOS(t *testing.T) {
	if os.Getenv("EGGIFY_CODEX_SANDBOX_PROBE") != "1" {
		t.Skip("set EGGIFY_CODEX_SANDBOX_PROBE=1 on a host that permits the OS sandbox")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("requires macOS or Linux")
	}
	codex, err := exec.LookPath("codex")
	require.NoError(t, err)
	python, err := exec.LookPath("python3")
	require.NoError(t, err)
	npm, err := exec.LookPath("npm")
	require.NoError(t, err, "npm is required to exercise package installation")
	ssh, err := exec.LookPath("ssh")
	require.NoError(t, err)
	// Deliberately outside /tmp: the built-in workspace profile allows temp writes.
	fixture, err := os.MkdirTemp(".", ".eggify-network-probe-")
	require.NoError(t, err)
	fixture, err = filepath.Abs(fixture)
	require.NoError(t, err)
	defer os.RemoveAll(fixture)
	fixture, err = filepath.EvalSymlinks(fixture)
	require.NoError(t, err)
	for _, tempRoot := range []string{os.TempDir(), "/tmp", "/private/tmp"} {
		resolved, err := filepath.EvalSymlinks(tempRoot)
		if os.IsNotExist(err) {
			continue
		}
		require.NoError(t, err)
		rel, err := filepath.Rel(resolved, fixture)
		require.NoError(t, err)
		require.True(t, rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)),
			"run this probe from a checkout outside temporary directories: %s allows writes by design", resolved)
	}
	home := filepath.Join(fixture, "home")
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".claude", "eggify"), 0700))
	secret := filepath.Join(home, ".claude", "eggify", ".secret")
	require.NoError(t, os.WriteFile(secret, []byte("synthetic-secret"), 0600))
	outside := filepath.Join(fixture, "outside.txt")
	require.NoError(t, os.WriteFile(outside, []byte("unchanged"), 0600))
	// A tiny package served locally exercises npm's real HTTP/download/extract
	// path without reaching a public registry or running any install scripts.
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	pkg := []byte(`{"name":"eggify-network-probe","version":"1.0.0"}`)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "package/package.json", Mode: 0600, Size: int64(len(pkg))}))
	_, err = tw.Write(pkg)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive.Bytes())
	}))
	defer server.Close()
	url := strings.Replace(server.URL, "127.0.0.1", "localhost", 1) + "/package.tgz"
	// A local SSH banner tests OpenSSH's direct TCP transport without logging
	// into any machine or presenting credentials. Key exchange then stops.
	sshListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer sshListener.Close()
	go func() {
		for {
			conn, err := sshListener.Accept()
			if err != nil {
				return
			}
			_, _ = conn.Write([]byte("SSH-2.0-EggifyProbe\r\n"))
			_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			_, _ = bufio.NewReader(conn).ReadString('\n')
			_ = conn.Close()
		}
	}()
	_, sshPort, err := net.SplitHostPort(sshListener.Addr().String())
	require.NoError(t, err)
	for _, enabled := range []bool{false, true} {
		t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
			wd := filepath.Join(fixture, strconv.FormatBool(enabled))
			require.NoError(t, os.MkdirAll(filepath.Join(wd, ".git", "hooks"), 0700))
			for _, p := range []string{".mcp.json", ".git/config", ".git/hooks/pre-commit"} {
				require.NoError(t, os.WriteFile(filepath.Join(wd, p), []byte("protected"), 0600))
			}
			for _, p := range []string{"user.npmrc", "global.npmrc"} {
				require.NoError(t, os.WriteFile(filepath.Join(wd, p), nil, 0600))
			}
			cfg := codexSandboxConfig(home, wd, &SessionOptions{Sandboxed: boolp(true), CodexNetworkAccess: enabled})
			args := append([]string{"sandbox", "-P", codexSandboxProfile, "-C", wd}, codexProbeArgs(cfg)...)
			// HTTP connectivity checks DNS resolution and a TCP connection; npm
			// uses the same local endpoint. Filesystem checks run in BOTH modes.
			probe := `import os,sys,urllib.request,urllib.error,subprocess
enabled,secret,outside,url,npm,ssh,ssh_port=sys.argv[1:]
enabled=enabled=='true'
open('workspace-write.txt','w').write('ok')
try:
 open(secret).read()
 raise AssertionError('protected secret readable')
except PermissionError: pass
for p in [outside,'.mcp.json','.git/config','.git/hooks/pre-commit']:
 try:
  open(p,'w').write('BAD')
  raise AssertionError('write escaped protection: '+p)
 except PermissionError: pass
try:
 urllib.request.build_opener(urllib.request.ProxyHandler({})).open(url,timeout=5).read()
 reachable=True
except (OSError,urllib.error.URLError): reachable=False
assert reachable==enabled, ('network',reachable,enabled)
r=subprocess.run([ssh,'-vv','-F','/dev/null','-o','BatchMode=yes','-o','ConnectTimeout=3','-o','IdentityAgent=none','-o','IdentityFile=none','-o','UserKnownHostsFile=/dev/null','-o','GlobalKnownHostsFile=/dev/null','-p',ssh_port,'localhost','true'],stdout=subprocess.PIPE,stderr=subprocess.STDOUT,timeout=10)
assert (b'remote software version EggifyProbe' in r.stdout)==enabled, ('ssh transport',r.stdout.decode())
env={k:v for k,v in os.environ.items() if not k.lower().startswith('npm_config_') and k.lower() not in ('http_proxy','https_proxy','all_proxy','node_options')}
env.update(npm_config_fetch_retries='0',npm_config_fetch_timeout='5000',npm_config_update_notifier='false',NO_PROXY='*',no_proxy='*')
r=subprocess.run([npm,'install','--prefix','.','--ignore-scripts','--no-audit','--no-fund','--cache','./npm-cache','--userconfig','./user.npmrc','--globalconfig','./global.npmrc',url],env=env,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,timeout=30)
assert (r.returncode==0)==enabled, ('npm',r.returncode,r.stdout.decode())
if enabled: assert os.path.isfile('node_modules/eggify-network-probe/package.json')
print('network, SSH transport, npm install, workspace writes, secret denial and protected/outside writes verified')
`
			args = append(args, "--", python, "-c", probe, strconv.FormatBool(enabled), secret, outside, url, npm, ssh, sshPort)
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, codex, args...)
			cmd.Dir = wd
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, "OS sandbox probe failed (including inability to launch sandbox): %s", output)
			t.Log(string(output))
			data, err := os.ReadFile(outside)
			require.NoError(t, err)
			require.Equal(t, "unchanged", string(data))
		})
	}
}
