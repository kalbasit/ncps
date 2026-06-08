## 1. Argument parsing

- [x] 1.1 Add `--inflight-staging` (`action="store_true"`, default off) to the argparse setup in `main()`, with help text noting it only engages with a distributed locker (`--locker redis`)

## 2. Command assembly

- [x] 2.1 In the per-instance `cmd_app` build loop, append `--cache-inflight-staging-enabled` when `args.inflight_staging` is set; do not pass `retention`/`part-size` (rely on Go defaults)

## 3. Observability

- [x] 3.1 Add an "Inflight staging: enabled/disabled" line to the startup banner near the existing Mode/DB/Storage/Locker lines
- [x] 3.2 Add `"inflight_staging": args.inflight_staging` to `state_config` so it is persisted to `state.json`

## 4. Verification

- [x] 4.1 Run `run.py --inflight-staging` (single instance) and confirm `--cache-inflight-staging-enabled` appears on the spawned `serve` command and `state.json` shows `inflight_staging: true`
- [x] 4.2 Run `run.py` without the flag and confirm no `--cache-inflight-staging-*` arg is present and `state.json` shows `inflight_staging: false`
- [x] 4.3 Run `task fmt` and `task lint` and confirm both exit zero
