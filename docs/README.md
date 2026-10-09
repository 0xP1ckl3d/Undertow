# Undertow documentation

Undertow connects your operator client to remote agents and their networks through a server. The browser GUI is the primary interface for everyday use; the terminal console remains fully available. Start with [Getting started](getting-started.md) for your first deployment, then use the [GUI user guide](gui.md) to learn where features live and how to use them.

## Start here

| Guide | What you will learn |
| --- | --- |
| [Getting started](getting-started.md) | Build and initialise the server, connect a client, deploy a first agent, run a command, and reach a remote network |
| [GUI user guide](gui.md) | Find every sidebar page and agent tab; use commands, shells, files, tools, jobs, screenshots, routes, team tools, and settings |
| [Console guide](console.md) | Use both terminal consoles and understand command syntax shared with the GUI agent Console |
| [CLI reference](cli-reference.md) | Look up startup flags, lifecycle commands, diagnostic checks, and scripting commands |

## Find the task you are doing

| I want to… | Start here |
| --- | --- |
| Find an agent, inspect its host, run commands, or open a live shell | [GUI agent workflows](gui.md#choose-and-identify-an-agent) |
| Browse and transfer files | [GUI files and transfers](gui.md#browse-and-transfer-files) · [Console files](console.md#running-an-agent-program-and-transferring-files) |
| Run long-running work and keep its output | [GUI background jobs](gui.md#start-and-follow-background-jobs) · [Console jobs](console.md#server-console) |
| Select a Windows identity per operation or operator connection | [Authentication contexts / tokens](authentication-contexts.md) |
| Capture a Windows display | [GUI screenshots](gui.md#capture-and-view-screenshots) · [Console screenshots](console.md#windows-display-screenshots) |
| Understand quiet check-ins and why an agent stays connected | [Agent connection rhythm](gui.md#understand-agent-connection-rhythm) · [Idle sleep details](agent-distribution.md#idle-sleep) |
| Build, host, download, or deploy a headless agent | [Payload deployment](agent-distribution.md) |
| Jump a Windows agent through an existing source agent | [Windows Jump](windows-deployments.md) |
| Host a child payload through its parent's TCP relay or Windows SMB pipe | [Agent-hosted payloads](agent-distribution.md#host-a-payload-through-a-connected-agent) |
| Choose VPN egress, internal routing, or a carrier | [Networking modes and transports](networking-modes.md) |
| Configure carrier identities and agent callbacks | [Deployment profiles](deployment-profiles.md) |
| Use the console for agents, routes, files, jobs, screenshots, and forwards | [Console guide](console.md) |
| Bootstrap and manage operator accounts | [Operator authentication](operator-authentication.md) |
| Read the graph and understand agent paths | [GUI topology](gui.md#read-the-topology) |
| Coordinate with other operators | [GUI Team](gui.md#team-conversations-and-assignments) · [Console Team](console.md#team-coordination) |
| Review audit history, settings, or clean up records | [GUI settings and cleanup](gui.md#settings-history-and-cleanup) |
| Expose one or several local services through an agent | [Remote port forwarding](remote-port-forwarding.md) |
| Work through a complete network layout | [Deployment scenarios](scenarios.md) |
| Run an included module or load one in a session | [Module bank](module-bank.md) |
| Build and deploy a new module | [WASM](wasm-development.md), [native Windows](native-modules.md), [BOF](bof-compatibility.md), or [.NET assembly](assembly-modules.md) development |
| Connect child agents through a parent | [Topology and relays](topology-and-relays.md) |
| Connect a Windows child through an SMB named pipe | [Windows SMB named-pipe relays](smb-named-pipe-relays.md) |
| Use startup flags, terminal commands, or scripts | [CLI reference](cli-reference.md) |

## Develop modules

The module development guides explain formats, arguments, compatibility, and runtime limits:

| Format | Additional resources |
| --- | --- |
| [WASM](wasm-development.md) | [Packaged WASM tools](../modules/wasm/README.md) |
| [Native Windows modules](native-modules.md) | [Native SDK](../sdk/native/README.md) · [Native examples](../modules/native/README.md) |
| [BOFs](bof-compatibility.md) | [Packaged BOFs](../modules/bof/README.md) |
| [.NET Framework assemblies](assembly-modules.md) | [Packaged assemblies](../modules/assembly/README.md) |

## Reference and measurements

The [feature catalogue](../features.md) covers Undertow's capabilities in one place. For implementation and measurement, see the [protocol](protocol.md), [benchmark guide](benchmarks.md), and [raw benchmark results index](benchmark-results/README.md).

Back to [Undertow introduction](../README.md).
