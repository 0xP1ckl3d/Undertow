The tiny WebAssembly fixtures in this directory come from wazero v1.12.0
testdata (Apache-2.0; see `LICENSE-wazero`):

- `wasm_args.wasm`: `imports/wasi_snapshot_preview1/testdata/print_args.wasm`
- `wasm_loop.wasm`: `cmd/wazero/testdata/infinite_loop.wasm`
- `wasm_stdin.wasm`: `cmd/wazero/testdata/wasi_fd.wasm`
- `wasm_exit2.wasm`: `imports/wasi_snapshot_preview1/testdata/exit_on_start.wasm`

They are loaded as bytes by tests. Undertow's agent never reads a fixture path.
