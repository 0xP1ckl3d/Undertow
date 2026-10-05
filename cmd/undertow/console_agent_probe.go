package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// The operator runs this on the intended child workstation. It sends one
// certificate-pinned HEAD request through the actual relay listener, reads no
// artifact bytes, and does not start an agent or a process on the parent.
func printAgentHostProbeScript(out io.Writer, hosted hostedArtifactInfo, format string) error {
	if len(hosted.RetrievalPath) != 49 || !strings.HasPrefix(hosted.RetrievalPath, "/") {
		return errors.New("invalid relay retrieval path")
	}
	if decoded, err := hex.DecodeString(hosted.RetrievalPath[1:]); err != nil || len(decoded) != 24 {
		return errors.New("invalid relay retrieval path")
	}
	if decoded, err := hex.DecodeString(hosted.TLSCertSHA256); err != nil || len(decoded) != 32 {
		return errors.New("relay certificate pin is unavailable")
	}
	if format == "shell" {
		if hosted.PipePath != "" {
			return errors.New("SMB named-pipe verification requires PowerShell on Windows")
		}
		if hosted.TLSPublicKeyPin == "" {
			return errors.New("relay public key pin is unavailable")
		}
		fmt.Fprintf(out, "#!/bin/sh\nset -eu\n# Run on the intended child host. HEAD only; no file is downloaded or run.\ncurl --fail --silent --show-error --head --max-time 20 --noproxy '*' --insecure --pinnedpubkey 'sha256//%s' '%s' | grep -i '^X-Artifact-SHA256: %s' >/dev/null\nprintf 'Relay artifact delivery verified from this host. No payload was downloaded or started.\\n'\n", hosted.TLSPublicKeyPin, shQuote(hosted.Retrieval), hosted.Artifact.SHA256)
		return nil
	}
	if format != "powershell" {
		return errors.New("verification script must be powershell or shell")
	}
	if hosted.PipePath != "" {
		parts := strings.Split(hosted.PipePath[2:], `\`)
		if len(parts) != 3 || !strings.EqualFold(parts[1], "pipe") || parts[0] == "" || parts[2] == "" {
			return errors.New("invalid remote SMB pipe path")
		}
		fmt.Fprintf(out, "$pipeHost = '%s'\n$pipeName = '%s'\n", psQuote(parts[0]), psQuote(parts[2]))
	} else {
		u, err := url.Parse(hosted.Retrieval)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.Port() == "" || u.Path != hosted.RetrievalPath {
			return errors.New("invalid relay HTTPS URL")
		}
		fmt.Fprintf(out, "$relayHost = '%s'\n$relayPort = %s\n", psQuote(u.Hostname()), u.Port())
	}
	fmt.Fprintf(out, "$path = '%s'\n$certPin = '%s'\n$expected = '%s'\n", hosted.RetrievalPath, hosted.TLSCertSHA256, hosted.Artifact.SHA256)
	fmt.Fprint(out, `# Run on the intended child workstation. HEAD only; no payload is downloaded or started.
$ErrorActionPreference = 'Stop'
$factoryName = 'UndertowRelayProbe_' + [Guid]::NewGuid().ToString('N')
$source = @'
using System;
using System.IO;
using System.IO.Pipes;
using System.Net.Sockets;
using System.Net.Security;
using System.Security.Authentication;
using System.Security.Cryptography;
using System.Text;
public static class __FACTORY_NAME__ {
    public static void Tcp(string host, int port, string path, string pin, string expected) {
        using (var client = new TcpClient()) {
            var attempt = client.ConnectAsync(host, port);
            if (!attempt.Wait(15000)) throw new IOException("TCP relay is unreachable from this workstation");
            Verify(client.GetStream(), path, pin, expected);
        }
    }
    public static void Pipe(string host, string name, string path, string pin, string expected) {
        using (var pipe = new NamedPipeClientStream(host, name, PipeDirection.InOut, PipeOptions.None)) {
            pipe.Connect(15000);
            Verify(pipe, path, pin, expected);
        }
    }
    private static void Verify(Stream transport, string path, string pin, string expected) {
        using (var watchdog = new System.Threading.Timer(_ => transport.Dispose(), null, 20000, System.Threading.Timeout.Infinite))
        using (var tls = new SslStream(transport, false, (sender, certificate, chain, errors) => {
            if (certificate == null) return false;
            using (var sha = SHA256.Create()) {
                var actual = BitConverter.ToString(sha.ComputeHash(certificate.GetRawCertData())).Replace("-", "");
                return string.Equals(actual, pin, StringComparison.OrdinalIgnoreCase);
            }
        })) {
            tls.AuthenticateAsClient("undertow", null, SslProtocols.Tls12, false);
            var request = Encoding.ASCII.GetBytes("HEAD " + path + " HTTP/1.1\r\nHost: undertow\r\nConnection: close\r\n\r\n");
            tls.Write(request, 0, request.Length);
            tls.Flush();
            var header = new byte[8192];
            int count = 0;
            while (count < header.Length) {
                int b = tls.ReadByte();
                if (b < 0) throw new IOException("Relay closed before the verification response");
                header[count++] = (byte)b;
                if (count >= 4 && header[count-4] == 13 && header[count-3] == 10 && header[count-2] == 13 && header[count-1] == 10) break;
            }
            if (count == header.Length) throw new IOException("Relay verification response is too large");
            var lines = Encoding.ASCII.GetString(header, 0, count).Split(new string[] {"\r\n"}, StringSplitOptions.None);
            if (!lines[0].StartsWith("HTTP/1.1 200 ", StringComparison.Ordinal)) throw new IOException("Relay verification failed: " + lines[0]);
            bool hashMatches = false;
            foreach (var line in lines) {
                if (line.StartsWith("X-Artifact-SHA256:", StringComparison.OrdinalIgnoreCase)) {
                    hashMatches = string.Equals(line.Substring(18).Trim(), expected, StringComparison.OrdinalIgnoreCase);
                }
            }
            if (!hashMatches) throw new IOException("Relay returned an unexpected artifact hash");
        }
    }
}
'@
$source = $source.Replace('__FACTORY_NAME__', $factoryName)
if ($PSVersionTable.PSVersion.Major -lt 6) {
    $factory = Add-Type -ReferencedAssemblies System.Core -TypeDefinition $source -PassThru | Where-Object { $_.Name -eq $factoryName }
} else {
    $factory = Add-Type -TypeDefinition $source -PassThru | Where-Object { $_.Name -eq $factoryName }
}
`)
	if hosted.PipePath != "" {
		fmt.Fprint(out, "$factory::Pipe($pipeHost, $pipeName, $path, $certPin, $expected)\n")
	} else {
		fmt.Fprint(out, "$factory::Tcp($relayHost, $relayPort, $path, $certPin, $expected)\n")
	}
	fmt.Fprint(out, "Write-Host 'Relay artifact delivery verified from this workstation. No payload was downloaded or started.'\n")
	return nil
}
