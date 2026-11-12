# Known hardware quirks

Some drivers behave in ways fan2go can't detect reliably, but that are documented in the kernel docs or the driver
source. fan2go has a small built-in registry of such devices (`internal/quirks`). It applies an entry only to a
device it identifies exactly. It never guesses from observed behaviour.

## How it works

1. `hwmon.GetChips` reads the raw `name` file of every hwmon device (`HwMonController.HwmonName`, e.g.
   `corsaircpro`) and its `device/modalias`. `UpdateFanConfigFromHwMonControllers` copies both into the runtime
   part of the fan config (`HwMonFanConfig.HwmonName`, `Modalias`).
2. `fans.NewFan` looks up the entry for a hwmon fan (`quirks.ForFan`) and stores it in the fan.
   `fans.KnownQuirksOf(fan)` returns it, or nil for no entry and for non-hwmon fans.
3. The daemon logs one INFO line per fan with an entry, when it creates the fan (on start and on every reload).
   The line names the entry and its version, the effects, any effect the config overrides, a link to the related
   issue, and how to disable the entry.
4. The controller uses the entry as described in [Fields](#fields).
5. `fan2go config validate` matches the configured fans against the hardware in the same way. For every fan with an
   entry, it prints the entry's advice (tips and warnings). See [Advice](#advice-in-fan2go-config-validate).
6. `fan2go detect` shows the entry id in the `Quirks` column. The REST API shows
   `"quirks": {"id": ..., "version": ...}` in the JSON of a hwmon fan with an entry.

## Entries

| ID | hwmon `name` | Effect | Primary sources | Issues |
|---|---|---|---|---|
| `corsaircpro` | `corsaircpro` | `setPwmToGetPwmMap` from the driver formula: PWM is converted to percent with `DIV_ROUND_CLOSEST(val*100,255)` and read back with `DIV_ROUND_CLOSEST(pct*255,100)` (101 levels, e.g. 249 reads back as 250). | [docs](https://docs.kernel.org/hwmon/corsair-cpro.html), `corsair-cpro.c` `set_pwm`, `ccp_read` | #64, #132 |
| `aquacomputer` | `d5next`, `octo`, `quadro`, `aquaero` | `setPwmToGetPwmMap` is the identity. PWM is stored in centi-percent, and the round trip returns every value exactly. These four devices wait 200 ms between two control reports (`CTRL_REPORT_DELAY`), and every PWM read and write uses one, so measuring the map took minutes. | [docs](https://docs.kernel.org/hwmon/aquacomputer_d5next.html), `aquacomputer_d5next.c` `aqc_pwm_to_percent`, `aqc_percent_to_pwm` | #273, #324 |
| `thinkpad` | `thinkpad` | 8 EC levels (write `level = (pwm >> 5) & 7`), used for advice only. No readback formula: most models read back `level * 255 / 7`, but models with the ACPI `FANG`/`FANW` methods (e.g. E531) store the level in percent (`level * 100 / 7`, read `speed * 7 / 100`) and report other values, and the hwmon name is the same. Hint: fan control needs `thinkpad_acpi fan_control=1` (otherwise every write fails with EPERM). | [docs](https://docs.kernel.org/admin-guide/laptops/thinkpad-acpi.html), `thinkpad_acpi.c` `fan_pwm1_show`, `fan_pwm1_store`, `fan_set_level` | #47 |
| `nzxtsmart2` | `nzxtsmart2` | `setPwmToGetPwmMap` from the driver formula (percent, positive values never become 0). Hint: `pwm_enable` only accepts the value it reports (1 if the device detected a fan on the channel when the driver was loaded). | [docs](https://docs.kernel.org/hwmon/nzxt-smart2.html), `nzxt-smart2.c` `scale_pwm_value`, `set_pwm_enable` | none |

The source links with line numbers are in `internal/quirks/registry.go`.

## Fields

An entry (`quirks.Entry`) only has the fields that its devices need:

| Field | Used by | Effect |
|---|---|---|
| `Match.Names`, `Match.Modalias` | `quirks.Lookup` | Exact, case-sensitive hwmon `name`. Optionally, a `device/modalias` glob (`path.Match` syntax) to tell drivers with the same name apart. The most specific entry wins: name and modalias beat name only. |
| `Readback` | `computeSetPwmToGetPwmMap` | The set→get PWM map comes from the formula. fan2go doesn't write 0..255 to measure it, and doesn't store it in the database (like a configured `setPwmToGetPwmMap`). `Entry.OutputLevels()` returns the distinct values, i.e. the real output levels of the device. |
| `Levels` | advice | The number of output levels, if it is known but the readback formula isn't (ThinkPad: 8). `Entry.LevelCount()` returns it, or the number of `OutputLevels()`. `Entry.WriteStep()` is the smallest change of the written value that can change the output: the shortest inner run of written values with the same readback (Corsair: 2), or `256 / Levels` (ThinkPad: 32). |
| `Hint` | `takeManualControl` | Appended to the error when fan2go can't switch the fan to manual control: `... (known hardware quirk 'thinkpad': ...)`. |
| `Advice` | `fan2go config validate` | Functions that check a fan config for settings that don't work well with the device. |
| `ID`, `Version` | logs, `detect`, REST, database | `Version` is incremented on every change of `Readback` (see [Persistence](#persistence)). |

## Precedence and config

From highest to lowest:

1. Explicit per-fan config: `setPwmToGetPwmMap` in any mode, including an explicit `autodetect`. Also the
   `fan init --assume-pwm-map-identity` flag, but only for that run of `fan init`: the daemon uses the formula
   again. Set `setPwmToGetPwmMap: identity` to keep the identity.
2. `quirks.enabled: false` on the fan: no entry is applied to it.
3. The built-in entry.
4. Generic behaviour.

```yaml
fans:
  - id: pump
    hwMon:
      platform: d5next
      rpmChannel: 1
    quirks:
      enabled: false   # don't apply the known hardware quirks of this device (default: true)
```

`quirks` is only allowed on hwmon fans (validation error otherwise). There is no global switch, no way to force an
entry onto another device, and no user-defined entries.

## Advice in `fan2go config validate`

After a successful validation, `fan2go config validate` reads the hwmon devices like the daemon does (`GetChips`).
For each configured hwmon fan that it finds and that has an entry, it prints the entry's advice. A fan that can't be
found (e.g. no hardware, other machine) or has no entry is skipped silently. Tips are printed at INFO with a `Tip:`
prefix, warnings at WARNING. Neither changes the result or the exit code of the validation.

Each entry owns its advice (`Entry.Advice`, a list of `quirks.AdviceFunc`). The functions are in
`internal/quirks/advice.go`:

| Function | Entries | Level | Condition |
|---|---|---|---|
| `adviseReadbackFormula` | `corsaircpro`, `aquacomputer`, `nzxtsmart2` | tip | `setPwmToGetPwmMap` is configured although the formula is known |
| `adviseSmoothingResolution` | `corsaircpro`, `thinkpad`, `nzxtsmart2` | tip | A configured `controlAlgorithm.direct.deadband` is below one output step, or `rampUp`/`rampDown` moves less than one step per `adjustmentTickRate` cycle. One step is `WriteStep()` in written PWM units (ThinkPad 32, Corsair 2). One step in curve units is at least as large, so the tip only fires when the setting is certainly finer than one step. The default deadband of a fan without `controlAlgorithm` is not checked. |
| `adviseOnlyPwmControlMode` | `nzxtsmart2` | warning | `controlMode.active` or `controlMode.onExit.mode` is something other than `pwm`/`1` |
| `adviseThinkpadFanControl` | `thinkpad` | warning / tip | `/sys/module/thinkpad_acpi/parameters/fan_control` is `N` (warning). If it can't be read, a tip names the parameter. |

**Why**: the registry knows the device, so it can explain why a setting won't work before the daemon fails at
runtime. fan2go doesn't change the settings itself: smoothing defaults are not set by entries.

## Persistence

The analysis data of a hwmon fan (RPM curve, `setPwmToGetPwmMap`, `pwmMap`) is stored with the id and version of
the entry whose readback formula it was measured with. They are kept in the bucket `fanAnalysisInfo`, as
`{"quirk": "corsaircpro", "quirkVersion": 1}`, written after a successful analysis. If the formula isn't used (no
`Readback`, a configured `setPwmToGetPwmMap`, or `fan init --assume-pwm-map-identity`), the record is `("", 0)`:
hints, levels and advice don't affect the analysis.

Before it loads the saved data, `runInitializationIfNeeded` compares the stored values with the current entry. Data
without a record was measured without an entry, so it counts as `("", 0)`. If they differ, fan2go deletes the RPM
curve, the set→get map, the pwm map and the record, logs it at INFO, and analyzes the fan again. This happens when an
entry with a formula is added, its formula changes (version bump), it is removed, disabled with
`quirks.enabled: false`, or overridden in the config. A read error other than
missing/corrupt data stops the controller, like for the other analysis data. `fan2go fan init` and `fan reset` delete
the record together with the other data.

**Trade-off**: after an upgrade that adds an entry with a formula, the fans of that device are analyzed once more,
even if the old data was right.

## Devices without an entry

Reports that are not backed by a primary source, or that would need more fields, are not in the registry. They are
handled with the regular config:

- **Dell (`dell_smm`)**: the output levels depend on the model (`fan_max`), and the reported PWM changes only when
  the fan state changes, seconds later (#161). Use `sanityCheck.pwmValueChangedByThirdParty.enabled: false` and a
  `pwmMap`/`setPwmToGetPwmMap` with `values` for the states of your model (e.g. 0, 128, 255). On some machines the
  BIOS keeps changing the speed until its control is disabled (e.g. with `dell-bios-fan-control`).
- **AMD GPUs (`amdgpu`)**: the reported PWM may not follow the written value (#148, #196). Use
  `setPwmToGetPwmMap: identity` and disable the `pwmValueChangedByThirdParty` sanity check.
- **Corsair Commander Pro, fans that don't stop at 0 %** (#316): this depends on the fan. Set `minPwm` or a `pwmMap`
  so that stopping writes a value that the fan reacts to.
- **Out-of-tree NCT6687D driver** (`nct6687`): older versions cached register values for 1 s (#272, #358). The
  current driver reads the PWM register back right after a write, and the mainline `nct6683` driver uses the same
  hwmon name. If you still see stale values, set `pwmSetDelay` and `setPwmToGetPwmMap: identity`.
- **Mainline `nct6683`**: `pwm` is read-only, except on MiTAC boards. Use the out-of-tree driver for fan control.

## Adding an entry

- Identify the device exactly: the hwmon `name`, plus a modalias glob if another driver uses the same name.
- Every behaviour of the entry needs a primary source: a kernel doc or driver source link (pinned to a commit, with
  line numbers) as the first entry of `Sources`. Add related fan2go issues after it. A behaviour that is only known
  from a user report goes into [Devices without an entry](#devices-without-an-entry), not into the registry.
- Only add a field to `quirks.Entry` if the entry needs it, and document where it applies.
- Add advice functions for settings that the device rejects or that don't help on it.
- Increment `Version` on every change of an entry's `Readback` (not for hints, levels, advice, doc or source link
  changes). This makes fan2go analyze the affected fans again.
- Tests: registry invariants and formulas (`internal/quirks`), a fake sysfs tree in
  `internal/hwmon/testdata/sysfs/<device>` (`name`, `device/modalias`, `pwm1`, `fan1_input`, `pwm1_enable` if the
  driver has it), and advice tests.
- Update the tables in this document.

## Known limitations

- Only hwmon fans. NVIDIA fans handle their percent range in the backend.
- An entry applies to all channels of a device. There are no channel-specific entries.
- `thinkpad`: no readback formula, because it differs between models with the same hwmon name; the set/get sweep
  still runs (it is fast on the EC).
- No DMI matching, so model-specific behaviour (e.g. `dell_smm` levels) can't be expressed.
- `corsaircpro`: `pwm` can't be read before fan2go wrote it (kernel docs), so the original PWM isn't restored on exit
  (the device has no automatic mode anyway), and the `pwmValueChangedByThirdParty` check switches itself off on the
  first cycle.
- The `pwmMap` is still derived from the keys of the set→get map, not from the output levels, so quantized devices
  get an identity `pwmMap` (see [Fan analysis](fan-analysis.md)). `Entry.OutputLevels()` provides the levels for a
  later change.
