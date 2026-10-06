//go:build linux || windows

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"undertow/internal/agentprofile"
	"undertow/internal/control"
	"undertow/internal/mux"
	"undertow/internal/pivot"
)

type windowsDeploymentExecutor struct {
	manager      *control.Manager
	distribution *agentDistribution
}

type windowsDeploymentEndpoint struct {
	hosted       hostedArtifactInfo
	directShare  bool
	artifactPath string
}

var windowsInstallPath = regexp.MustCompile(`^[A-Za-z]:\\[^\x00-\x1f"<>|?*]+\.exe$`)

func normalizeWindowsInstallPath(record control.DeploymentRecord, requested, filename string) (string, error) {
	value := strings.TrimSpace(strings.ReplaceAll(requested, "/", `\`))
	if value == "" {
		value = `C:\ProgramData\Undertow\Deployments\` + record.ID + `\` + filename
	}
	if len(value) > 1024 || !windowsInstallPath.MatchString(value) {
		return "", errors.New("install path must be an absolute Windows .exe path")
	}
	for _, part := range strings.Split(value[3:], `\`) {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return "", errors.New("install path contains an invalid Windows path component")
		}
	}
	return value, nil
}

func (e *windowsDeploymentExecutor) Preflight(_ context.Context, record control.DeploymentRecord, request control.DeploymentStartRequest) (control.DeploymentExecutionPlan, error) {
	a, err := e.distribution.store.Artifact(record.ArtifactID)
	if err != nil || a.ID != record.ArtifactID || a.Revoked || a.Platform != "windows" || !strings.EqualFold(a.SHA256, record.ArtifactSHA256) {
		return control.DeploymentExecutionPlan{}, errors.New("selected Windows artifact is unavailable or changed")
	}
	artifactPath := e.distribution.store.ArtifactPath(a)
	if err := agentprofile.VerifyFile(artifactPath, record.ArtifactSHA256); err != nil {
		return control.DeploymentExecutionPlan{}, errors.New("selected Windows artifact failed integrity validation")
	}
	if record.Method == "service-control" && !a.ServiceCapable {
		return control.DeploymentExecutionPlan{}, errors.New("Service Control requires a service-capable Windows artifact")
	}
	if record.Context == "named-account" || record.Account != "" {
		return control.DeploymentExecutionPlan{}, errors.New("named-account deployments require a future credential integration")
	}
	path, err := normalizeWindowsInstallPath(record, request.InstallPath, a.Filename)
	if err != nil {
		return control.DeploymentExecutionPlan{}, err
	}
	selection := strings.TrimSpace(request.Delivery)
	var hosted hostedArtifactInfo
	deliveryType, deliveryID := "", ""
	switch {
	case selection == "direct-share":
		deliveryType, deliveryID = "direct-share", record.SourceAgentID
	case selection == "server":
		if !a.Hosted {
			return control.DeploymentExecutionPlan{}, errors.New("selected artifact is not hosted on the server")
		}
		hosted, err = e.distribution.hostedInfo(a)
		if err != nil || !strings.HasPrefix(hosted.Retrieval, "https://") {
			return control.DeploymentExecutionPlan{}, errors.New("server artifact host is unavailable")
		}
		deliveryType, deliveryID = "server", "server"
	case strings.HasPrefix(selection, "agent-host:"):
		id := strings.TrimPrefix(selection, "agent-host:")
		host, ok := e.distribution.agentHost(id)
		if !ok || id == "" {
			return control.DeploymentExecutionPlan{}, errors.New("selected agent artifact host is unavailable")
		}
		if host.ArtifactID != record.ArtifactID || host.AgentID != record.SourceAgentID {
			return control.DeploymentExecutionPlan{}, errors.New("agent artifact host must serve this build from the selected source agent")
		}
		hosted = hostedArtifactInfo{Artifact: a, Retrieval: host.Retrieval, RetrievalPath: host.RetrievalPath, PipePath: host.PipePath, TLSSelfSigned: host.TLSSelfSigned, TLSCertSHA256: host.TLSCertSHA256, TLSPublicKeyPin: host.TLSPublicKeyPin}
		deliveryType, deliveryID = "agent-host", id
	default:
		return control.DeploymentExecutionPlan{}, errors.New("choose direct-share, server, or agent-host:HOST_ID delivery")
	}
	return control.DeploymentExecutionPlan{DeliveryType: deliveryType, DeliveryID: deliveryID, InstallPath: path, Opaque: windowsDeploymentEndpoint{hosted: hosted, directShare: deliveryType == "direct-share", artifactPath: artifactPath}}, nil
}

func (e *windowsDeploymentExecutor) Start(ctx context.Context, record control.DeploymentRecord, plan control.DeploymentExecutionPlan, source *mux.Mux, report func(control.DeploymentProgress) error) error {
	endpoint, ok := plan.Opaque.(windowsDeploymentEndpoint)
	if !ok {
		return errors.New("Windows deployment endpoint is unavailable")
	}
	transferID := ""
	if endpoint.directShare {
		sharePath, err := windowsAdminSharePath(record.Target, plan.InstallPath)
		if err != nil {
			return err
		}
		prepare := `$path='` + psQuote(sharePath) + `'; [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($path)) | Out-Null`
		result, err := pivot.Execute(ctx, source, []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-Command", prepare})
		if err != nil || result.Error != "" || result.ExitCode != 0 {
			return errors.New("source agent could not prepare the target administrative share")
		}
		transfer, history, err := e.manager.StreamDeploymentArtifact(ctx, record.SourceAgentID, source, endpoint.artifactPath, sharePath)
		transferID = history.ID
		if err != nil {
			if transferID != "" {
				_ = report(control.DeploymentProgress{State: "failed", Progress: "Artifact stream failed", Failure: "The source agent could not stream the artifact to the target administrative share. Inspect the linked Transfer.", TransferID: transferID, InstallPath: plan.InstallPath})
			}
			return fmt.Errorf("stream artifact through source agent to target share: %w", err)
		}
		if !strings.EqualFold(transfer.SHA256, record.ArtifactSHA256) {
			_ = report(control.DeploymentProgress{State: "failed", Progress: "Artifact stream failed", Failure: "The completed Transfer checksum did not match the selected build.", TransferID: transferID, InstallPath: plan.InstallPath})
			return errors.New("direct-share artifact checksum did not match the selected build")
		}
	}
	script, serviceName, taskName, err := buildWindowsDeploymentScript(record, plan.InstallPath, endpoint.hosted, endpoint.directShare)
	if err != nil {
		if transferID != "" {
			_ = report(control.DeploymentProgress{State: "failed", Progress: "Method preparation failed", Failure: err.Error(), TransferID: transferID, InstallPath: plan.InstallPath})
		}
		return err
	}
	job, err := e.manager.StartDeploymentScriptJob(ctx, record.SourceAgentID, record.ID, script)
	if err != nil {
		if transferID != "" {
			_ = report(control.DeploymentProgress{State: "failed", Progress: "Method Job start failed", Failure: "The artifact Transfer completed, but the Windows method Job could not start.", TransferID: transferID, InstallPath: plan.InstallPath})
		}
		return err
	}
	progress := control.DeploymentProgress{State: "waiting", Progress: "Windows method Job accepted; waiting for target enrolment", JobID: job.ID, TransferID: transferID, InstallPath: plan.InstallPath, ServiceName: serviceName, TaskName: taskName}
	if err := report(progress); err != nil {
		return err
	}
	go e.monitor(record.ID, job.ID, report)
	return nil
}

func (e *windowsDeploymentExecutor) monitor(_ string, jobID string, report func(control.DeploymentProgress) error) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		job, err := e.manager.Job(0, jobID, false)
		if err != nil {
			return
		}
		switch job.State {
		case "completed":
			_ = report(control.DeploymentProgress{State: "waiting", Progress: "Windows method completed; waiting for target enrolment", JobID: jobID})
			return
		case "failed":
			_ = report(control.DeploymentProgress{State: "failed", Progress: "Windows method failed", Failure: "The source-agent Job failed. Inspect the linked Job for its sanitized result.", JobID: jobID})
			return
		case "cancelled":
			_ = report(control.DeploymentProgress{State: "failed", Progress: "Windows method cancelled", Failure: "The linked source-agent Job was cancelled.", JobID: jobID})
			return
		case "interrupted":
			_ = report(control.DeploymentProgress{State: "waiting", Progress: "Windows method Job was interrupted; execution outcome is uncertain. Review before retrying.", JobID: jobID})
			return
		}
	}
}

func powershellArtifactHelper(hosted hostedArtifactInfo, launch bool) (string, error) {
	var out bytes.Buffer
	if err := printDeployScript(&out, hosted, "powershell"); err != nil {
		return "", err
	}
	if launch {
		return out.String(), nil
	}
	const start = "  Start-Process -FilePath $Destination -WindowStyle Hidden\n"
	value := out.String()
	if !strings.Contains(value, start) {
		return "", errors.New("PowerShell payload helper has an unexpected launch step")
	}
	return strings.Replace(value, start, "", 1), nil
}

func windowsAdminSharePath(target, installPath string) (string, error) {
	if !windowsInstallPath.MatchString(installPath) {
		return "", errors.New("invalid Windows install path")
	}
	return `\\` + target + `\` + strings.ToUpper(installPath[:1]) + `$\` + installPath[3:], nil
}

func buildWindowsDeploymentScript(record control.DeploymentRecord, installPath string, hosted hostedArtifactInfo, directShare bool) ([]byte, string, string, error) {
	launchHelper, downloadHelper := "", ""
	var err error
	if !directShare {
		launchHelper, err = powershellArtifactHelper(hosted, true)
		if err != nil {
			return nil, "", "", err
		}
		downloadHelper, err = powershellArtifactHelper(hosted, false)
		if err != nil {
			return nil, "", "", err
		}
	}
	encodedLaunch := base64.StdEncoding.EncodeToString([]byte(launchHelper))
	encodedDownload := base64.StdEncoding.EncodeToString([]byte(downloadHelper))
	shortID := record.ID
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}
	serviceName := ""
	taskName := ""
	var body strings.Builder
	fmt.Fprintf(&body, "$ErrorActionPreference = 'Stop'\n$target = '%s'\n$destination = '%s'\n", psQuote(record.Target), psQuote(installPath))
	body.WriteString("try {\n")
	switch record.Method {
	case "winrm":
		if directShare {
			body.WriteString("  Invoke-Command -ComputerName $target -ErrorAction Stop -ArgumentList $destination -ScriptBlock { param($path) Start-Process -FilePath $path -WindowStyle Hidden }\n")
		} else {
			fmt.Fprintf(&body, "  $helper = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s'))\n", encodedLaunch)
			body.WriteString("  Invoke-Command -ComputerName $target -ErrorAction Stop -ArgumentList $helper,$destination -ScriptBlock { param($source,$path) try { [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($path)) | Out-Null; & ([ScriptBlock]::Create($source)) -Destination $path } catch { throw 'Target payload launch failed.' } }\n")
		}
		body.WriteString("  Write-Output 'WinRM accepted and completed the verified deployment helper.'\n")
	case "wmi", "scheduled-task":
		if directShare {
			body.WriteString("  $command = '\"' + $destination + '\"'\n")
			if record.Method == "wmi" {
				body.WriteString("  $result = ([WMIClass]('\\\\' + $target + '\\root\\cimv2:Win32_Process')).Create($command)\n  if ($result.ReturnValue -ne 0) { throw 'WMI rejected the deployment process.' }\n  Write-Output 'WMI started the streamed agent binary.'\n")
			} else {
				taskName = `\Undertow\Deploy-` + shortID
				fmt.Fprintf(&body, "  $taskName = '%s'\n", psQuote(taskName))
				if record.Context == "local-system" {
					body.WriteString("  & schtasks.exe /Create /S $target /TN $taskName /SC ONCE /ST 00:00 /TR $command /RU SYSTEM /RL HIGHEST /F | Out-Null\n")
				} else {
					body.WriteString("  $runAs = $env:USERDOMAIN + '\\' + $env:USERNAME\n  & schtasks.exe /Create /S $target /TN $taskName /SC ONCE /ST 00:00 /TR $command /RU $runAs /IT /NP /RL HIGHEST /F | Out-Null\n")
				}
				body.WriteString("  if ($LASTEXITCODE -ne 0) { throw 'Scheduled Task creation failed.' }\n  & schtasks.exe /Run /S $target /TN $taskName | Out-Null\n  if ($LASTEXITCODE -ne 0) { throw 'Scheduled Task start failed.' }\n  Start-Sleep -Seconds 2\n  & schtasks.exe /Delete /S $target /TN $taskName /F | Out-Null\n  Write-Output 'Scheduled Task started the streamed agent binary.'\n")
			}
			break
		}
		remoteHelper := `C:\Windows\Temp\Undertow-` + shortID + `.ps1`
		remoteOK := `C:\Windows\Temp\Undertow-` + shortID + `.ok`
		remoteFail := `C:\Windows\Temp\Undertow-` + shortID + `.failed`
		shareBase := `\\` + record.Target + `\ADMIN$\Temp\Undertow-` + shortID
		staged := stagedWindowsHelper(launchHelper, remoteOK, remoteFail)
		encodedStaged := base64.StdEncoding.EncodeToString([]byte(staged))
		fmt.Fprintf(&body, "  $staged = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s'))\n", encodedStaged)
		fmt.Fprintf(&body, "  $helperShare = '%s.ps1'; $okShare = '%s.ok'; $failedShare = '%s.failed'\n", psQuote(shareBase), psQuote(shareBase), psQuote(shareBase))
		fmt.Fprintf(&body, "  $remoteHelper = '%s'\n", psQuote(remoteHelper))
		body.WriteString("  Set-Content -LiteralPath $helperShare -Value $staged -Encoding UTF8 -Force\n")
		body.WriteString("  $command = 'powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File \"' + $remoteHelper + '\" -Destination \"' + $destination + '\"'\n")
		if record.Method == "wmi" {
			body.WriteString("  $result = ([WMIClass]('\\\\' + $target + '\\root\\cimv2:Win32_Process')).Create($command)\n  if ($result.ReturnValue -ne 0) { throw 'WMI rejected the deployment process.' }\n")
		} else {
			taskName = `\Undertow\Deploy-` + shortID
			fmt.Fprintf(&body, "  $taskName = '%s'\n", psQuote(taskName))
			if record.Context == "local-system" {
				body.WriteString("  & schtasks.exe /Create /S $target /TN $taskName /SC ONCE /ST 00:00 /TR $command /RU SYSTEM /RL HIGHEST /F | Out-Null\n")
			} else {
				body.WriteString("  $runAs = $env:USERDOMAIN + '\\' + $env:USERNAME\n  & schtasks.exe /Create /S $target /TN $taskName /SC ONCE /ST 00:00 /TR $command /RU $runAs /IT /NP /RL HIGHEST /F | Out-Null\n")
			}
			body.WriteString("  if ($LASTEXITCODE -ne 0) { throw 'Scheduled Task creation failed.' }\n  & schtasks.exe /Run /S $target /TN $taskName | Out-Null\n  if ($LASTEXITCODE -ne 0) { throw 'Scheduled Task start failed.' }\n")
		}
		body.WriteString("  $deadline = [DateTime]::UtcNow.AddMinutes(3)\n  while ([DateTime]::UtcNow -lt $deadline -and -not (Test-Path -LiteralPath $okShare) -and -not (Test-Path -LiteralPath $failedShare)) { Start-Sleep -Milliseconds 500 }\n  if (Test-Path -LiteralPath $failedShare) { throw 'Target deployment helper reported failure.' }\n  if (-not (Test-Path -LiteralPath $okShare)) { throw 'Timed out waiting for the target deployment helper.' }\n")
		body.WriteString("  Write-Output 'The target completed the verified deployment helper.'\n")
		body.WriteString("} catch { Write-Error ('Windows deployment failed during method execution.'); exit 1 } finally {\n")
		if record.Method == "scheduled-task" {
			body.WriteString("  if ($taskName) { & schtasks.exe /Delete /S $target /TN $taskName /F 2>$null | Out-Null }\n")
		}
		body.WriteString("  Remove-Item -LiteralPath $helperShare,$okShare,$failedShare -Force -ErrorAction SilentlyContinue\n}\n")
		return []byte(body.String()), serviceName, taskName, nil
	case "service-control":
		serviceName = "Undertow-" + shortID
		fmt.Fprintf(&body, "  $serviceName = '%s'\n", psQuote(serviceName))
		if !directShare {
			fmt.Fprintf(&body, "  $helper = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s'))\n", encodedDownload)
			body.WriteString("  if ($destination -notmatch '^([A-Za-z]):\\\\(.+)$') { throw 'Invalid service destination.' }\n  $sharePath = '\\\\' + $target + '\\' + $Matches[1] + '$\\' + $Matches[2]\n  $shareDirectory = [IO.Path]::GetDirectoryName($sharePath)\n  New-Item -ItemType Directory -Path $shareDirectory -Force | Out-Null\n  $temporary = Join-Path $env:TEMP ('undertow-' + [Guid]::NewGuid().ToString('N') + '.exe')\n  try {\n    & ([ScriptBlock]::Create($helper)) -Destination $temporary\n    Copy-Item -LiteralPath $temporary -Destination $sharePath -Force\n  } finally { Remove-Item -LiteralPath $temporary -Force -ErrorAction SilentlyContinue }\n")
		}
		body.WriteString("  & sc.exe ('\\\\' + $target) create $serviceName 'binPath=' ('\"' + $destination + '\"') 'start=' auto 'obj=' LocalSystem | Out-Null\n  if ($LASTEXITCODE -ne 0) { throw 'Service Control creation failed.' }\n  & sc.exe ('\\\\' + $target) start $serviceName | Out-Null\n  if ($LASTEXITCODE -ne 0) { throw 'Service Control start failed.' }\n  Write-Output 'Service Control installed and started the verified agent service.'\n")
	default:
		return nil, "", "", errors.New("unsupported Windows deployment method")
	}
	body.WriteString("} catch { Write-Error 'Windows deployment failed during method execution.'; exit 1 }\n")
	return []byte(body.String()), serviceName, taskName, nil
}

func stagedWindowsHelper(helper, okPath, failedPath string) string {
	parts := strings.SplitN(helper, "\n", 2)
	body := helper
	if len(parts) == 2 && strings.HasPrefix(parts[0], "param(") {
		body = parts[1]
	}
	return fmt.Sprintf("param([string]$Destination)\ntry {\n  [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($Destination)) | Out-Null\n%s\n  Set-Content -LiteralPath '%s' -Value 'ok' -Encoding ASCII -Force\n} catch { Set-Content -LiteralPath '%s' -Value 'failed' -Encoding ASCII -Force; exit 1 } finally { Remove-Item -LiteralPath $MyInvocation.MyCommand.Path -Force -ErrorAction SilentlyContinue }\n", body, psQuote(okPath), psQuote(failedPath))
}
