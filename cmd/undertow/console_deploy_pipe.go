package main

import (
	"fmt"
	"io"
)

// printPipeDeployDownload emits a Windows-only, operator-run helper. The pipe
// listener is already active and multiplexes this pinned TLS request with
// normal child sessions; the agent fetches the bytes from the server on demand.
func printPipeDeployDownload(out io.Writer) {
	fmt.Fprint(out, `  $factoryName = 'UndertowPipeDownload_' + [Guid]::NewGuid().ToString('N')
  $source = @'
using System;
using System.Globalization;
using System.IO;
using System.IO.Pipes;
using System.Net.Security;
using System.Security.Authentication;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using System.Text;
public static class __FACTORY_NAME__ {
    public static void Download(string server, string name, string token, string pin, string expected, string path) {
        using (var pipe = new NamedPipeClientStream(server, name, PipeDirection.InOut, PipeOptions.None)) {
            pipe.Connect(15000);
            using (var watchdog = new System.Threading.Timer(_ => pipe.Dispose(), null, 30000, System.Threading.Timeout.Infinite)) {
            using (var tls = new SslStream(pipe, false, (sender, certificate, chain, errors) => {
                if (certificate == null) return false;
                using (var sha = SHA256.Create()) {
                    var actual = BitConverter.ToString(sha.ComputeHash(certificate.GetRawCertData())).Replace("-", "");
                    return string.Equals(actual, pin, StringComparison.OrdinalIgnoreCase);
                }
            })) {
                try { tls.AuthenticateAsClient("undertow", null, SslProtocols.Tls12, false); }
                catch (Exception ex) { throw new IOException("Pipe TLS handshake failed: " + ex.ToString(), ex); }
                watchdog.Change(600000, System.Threading.Timeout.Infinite);
                var request = Encoding.ASCII.GetBytes("GET /" + token + " HTTP/1.1\r\nHost: undertow\r\nConnection: close\r\n\r\n");
                tls.Write(request, 0, request.Length);
                tls.Flush();
                var header = new byte[8192];
                int count = 0;
                while (count < header.Length) {
                    int b = tls.ReadByte();
                    if (b < 0) throw new IOException("Pipe download ended before the response header");
                    header[count++] = (byte)b;
                    if (count >= 4 && header[count-4] == 13 && header[count-3] == 10 && header[count-2] == 13 && header[count-1] == 10) break;
                }
                if (count == header.Length) throw new IOException("Pipe download response header is too large");
                var lines = Encoding.ASCII.GetString(header, 0, count).Split(new string[] {"\r\n"}, StringSplitOptions.None);
                if (!lines[0].StartsWith("HTTP/1.1 200 ", StringComparison.Ordinal)) throw new IOException("Pipe download failed: " + lines[0]);
                long size = -1;
                string hash = null;
                foreach (var line in lines) {
                    if (line.StartsWith("Content-Length:", StringComparison.OrdinalIgnoreCase)) {
                        if (!long.TryParse(line.Substring(15).Trim(), NumberStyles.None, CultureInfo.InvariantCulture, out size)) throw new IOException("Invalid pipe download size");
                    }
                    if (line.StartsWith("X-Artifact-SHA256:", StringComparison.OrdinalIgnoreCase)) hash = line.Substring(18).Trim();
                }
                if (size < 0 || size > 512L * 1024L * 1024L || !string.Equals(hash, expected, StringComparison.OrdinalIgnoreCase)) throw new IOException("Pipe download metadata mismatch");
                using (var target = new FileStream(path, FileMode.Create, FileAccess.Write, FileShare.None)) {
                    var buffer = new byte[65536];
                    while (size > 0) {
                        int n = tls.Read(buffer, 0, (int)Math.Min((long)buffer.Length, size));
                        if (n <= 0) throw new IOException("Pipe download ended before all bytes arrived");
                        target.Write(buffer, 0, n);
                        size -= n;
                    }
                }
            }
            }
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
  $factory::Download($pipeHost, $pipeName, $token, $certPin, $expected, $temp)
`)
}
