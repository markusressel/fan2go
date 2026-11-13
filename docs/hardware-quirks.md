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
| `it87` | `it8603`, `it8606`, `it8613`, `it8622`, `it8625`, `it8655`, `it8665`, `it8686`, `it8688`, `it8689`, `it8696`, `it8698` (all channels); `it8620`, `it8628`, `it8732` (`pwm4` and above). Modalias `platform:it87*` only. | `pwm_enable` 0 together with `pwm` 255 counts as manual mode (see [it87: pwm_enable 0 at full duty](#it87-pwm_enable-0-at-full-duty)). Hint: on channels with fan on/off control, `pwm_enable` 0 means full speed without PWM control. | `it87.c` `pwm_mode`, `it87_devices`, `set_pwm`; out-of-tree [frankcrawford/it87](https://github.com/frankcrawford/it87) `it87.c` `pwm_mode`, `it87_devices` | none |

The source links with line numbers are in `internal/quirks/registry.go`.

## Fields

An entry (`quirks.Entry`) only has the fields that its devices need:

| Field | Used by | Effect |
|---|---|---|
| `Match.Names`, `Match.Modalias` | `quirks.Lookup` | Exact, case-sensitive hwmon `name`. Optionally, a `device/modalias` glob (`path.Match` syntax) to tell drivers with the same name apart. The most specific entry wins: name and modalias beat name only. |
| `Readback` | `computeSetPwmToGetPwmMap`, `computePwmMapAutomatically` | The set→get PWM map comes from the formula. fan2go doesn't write 0..255 to measure it, and doesn't store it in the database (like a configured `setPwmToGetPwmMap`). `Entry.OutputLevels()` returns the distinct values, i.e. the real output levels of the device. The autodetected `pwmMap` uses one written value per output level (the one that reads back as itself), so the analysis measures each level once (Corsair: 101 target ranges). |
| `Levels` | advice | The number of output levels, if it is known but the readback formula isn't (ThinkPad: 8). `Entry.LevelCount()` returns it, or the number of `OutputLevels()`. `Entry.WriteStep()` is the smallest change of the written value that can change the output: the shortest inner run of written values with the same readback (Corsair: 2), or `256 / Levels` (ThinkPad: 32). |
| `Hint` | `takeManualControl` | Appended to the error when fan2go can't switch the fan to manual control: `... (known hardware quirk 'thinkpad': ...)`. |
| `FullDutyReadsAsFullSpeedFromChannel` | `HwMonFan.GetControlMode`, `HwMonFan.SetControlMode`, `HwMonFan.SetPwm` | Per hwmon name, the first `pwm` channel whose `pwm_enable` reads 0 instead of 1 in manual mode at `pwm` 255. `Entry.FullDutyReadsAsFullSpeed(name, channel)` checks a channel. See [it87: pwm_enable 0 at full duty](#it87-pwm_enable-0-at-full-duty). |
| `Advice` | `fan2go config validate` | Functions that check a fan config for settings that don't work well with the device. |
| `ID`, `Version` | logs, `detect`, REST, database | `Version` is incremented on every change of `Readback` (see [Persistence](#persistence)). |

## it87: pwm_enable 0 at full duty

`pwm_mode()` in the it87 driver (mainline and [frankcrawford/it87](https://github.com/frankcrawford/it87)) doesn't
store the mode that was written. It derives `pwm_enable` from the registers:

```c
if (has_fanctl_onoff(data) && nr < 3 && !(data->fan_main_ctrl & BIT(nr)))
	return 0;			/* Full speed */
if (data->pwm_ctrl[nr] & 0x80)
	return 2;			/* Automatic mode */
if ((!has_fanctl_onoff(data) || nr >= 3) && data->pwm_duty[nr] == pwm_to_reg(data, 0xff))
	return 0;			/* Full speed */
return 1;				/* Manual mode */
```

On chips without `FEAT_FANCTL_ONOFF`, and on `pwm4` and above (`nr >= 3`) of chips with it, a channel in manual mode
at duty 255 reads `pwm_enable` 0. Writing 1 to `pwm_enable` at `pwm` 255 reads back 0, and writing 128 to `pwm` then
reads back 1. Writing 0 sets the duty to 255 in manual mode, so 0 and "manual at 255" are the same hardware state.
Without the entry, fan2go fails with `PWM mode stuck to 0` whenever the duty is 255 when it switches to manual mode
(e.g. the board's automatic curve drove the fan to 255), and the `fanModeChangedByThirdParty` sanity check reports
`Fan mode of fan X is ... (expected PWM)` whenever fan2go's curve asks for 255.

For a fan of a listed chip and channel, fan2go treats `pwm_enable` 0 as manual mode if `pwm` reads 255:

1. `SetControlMode(pwm)` succeeds if `pwm_enable` reads back 0 and `pwm` reads 255.
2. `GetControlMode` returns `pwm` for `pwm_enable` 0 with `pwm` 255, so the sanity check (and `fan2go detect`) sees
   manual mode. It checks the current `pwm`, not the value fan2go wrote last: the driver derives the 0 from the
   current duty only, and fan2go may not have written anything yet (start-up, restore).
3. Every time fan2go takes 0 as manual mode, the next write of a `pwm` value below 255 must confirm it: `pwm_enable`
   must read 1 then. If it doesn't, the write returns an error (`... PWM mode stuck to 0`), handled like other PWM
   write errors. Afterwards `pwm` isn't 255, so the sanity check sees `pwm_enable` 0 as disabled again and switches
   the fan to manual mode, which fails with `PWM mode stuck to 0` if the channel really is in on/off mode.
4. fan2go keeps writing 255. It doesn't write 254 to keep `pwm_enable` at 1. This includes a kick at full speed
   when a fan starts from standstill (see [fan-controller.md](fan-controller.md#kick-start-from-standstill)): the
   `pwm_enable` 0 it reads during the kick is taken as manual mode, and the write of the target after it confirms it.

fan2go already switches a fan to manual mode before it writes `pwm` (`takeManualControl` before the analysis and the
control loop, the sanity check before every write). This matters on these chips: `set_pwm` returns EBUSY in automatic
mode. Only the fallbacks of the restore on exit write `pwm` after a failed mode switch.

The entry is limited to the it87 driver (modalias `platform:it87*`). On other drivers, and on `pwm1`-`pwm3` of chips
with fan on/off control, `pwm_enable` 0 can mean "full speed, no PWM control", so fan2go keeps reporting
`PWM mode stuck to 0` there. The chip list is the union of both drivers. Chips that only one of them supports are
included, and `it8689` (fan on/off control in mainline, none in frankcrawford/it87, same hwmon name) is matched on all
channels: the confirmation in step 3 catches a channel that is really in on/off mode.

## Precedence and config

From highest to lowest:

1. Explicit per-fan config: `setPwmToGetPwmMap` with `identity`, `linear` or `values`. Also the
   `fan init --assume-pwm-map-identity` flag, but only for that run of `fan init`: the daemon uses the formula
   again (without analyzing the fan again, see [Persistence](#persistence)). Set `setPwmToGetPwmMap: identity` to
   keep the identity. `setPwmToGetPwmMap: autodetect` is the same as
   no `setPwmToGetPwmMap`: the formula is used.
2. `quirks.enabled: false` on the fan: no entry is applied to it, so `autodetect` measures the map with the sweep.
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
| `adviseReadbackFormula` | `corsaircpro`, `aquacomputer`, `nzxtsmart2` | tip | `setPwmToGetPwmMap` is set to `identity`, `linear` or `values` although the formula is known |
| `adviseSmoothingResolution` | `corsaircpro`, `thinkpad`, `nzxtsmart2` | tip | A configured `controlAlgorithm.direct.deadband` is below one output step, or `rampUp`/`rampDown` moves less than one step per `adjustmentTickRate` cycle. One step is `WriteStep()` in written PWM units (ThinkPad 32, Corsair 2). One step in curve units is at least as large, so the tip only fires when the setting is certainly finer than one step. The deadband tip recommends one step only if that is at most 10% (25.5, the limit above which validation warns); otherwise it explains the trade-off. The ramp tip only fires if one step takes at least 2 s. The default deadband of a fan without `controlAlgorithm` is not checked. |
| `adviseOnlyPwmControlMode` | `nzxtsmart2` | warning | `controlMode.active` or `controlMode.onExit.mode` is something other than `pwm`/`1` |
| `adviseThinkpadFanControl` | `thinkpad` | warning / tip | `/sys/module/thinkpad_acpi/parameters/fan_control` is `N` (warning). If it can't be read, a tip names the parameter. |

**Why**: the registry knows the device, so it can explain why a setting won't work before the daemon fails at
runtime. fan2go doesn't change the settings itself: smoothing defaults are not set by entries.

## Persistence

The analysis data of a hwmon fan (RPM curve, `setPwmToGetPwmMap`, `pwmMap`) is stored with the id and version of
the entry whose readback formula it was measured with. They are kept in the bucket `fanAnalysisInfo`, as
`{"quirk": "corsaircpro", "quirkVersion": 1}`, written after a successful analysis. If the formula isn't used (no
`Readback`, or a `setPwmToGetPwmMap` with `identity`/`linear`/`values`), the record is `("", 0)`: hints, levels and
advice don't affect the analysis. `fan init --assume-pwm-map-identity` still records the entry: the flag only applies
to that run, and the identity has the same keys (0..255) as the formula. The RPM curve and the `pwmMap` (the identity,
since the identity readback has 256 levels) were measured together, so the saved data stays valid when the daemon
uses the formula again.

The record also stores the readback delay measured during the analysis (`readbackDelayMillis`, see
[Fan analysis](fan-analysis.md)), also for hwmon fans without an entry. Fans whose PWM can't be read back (e.g.
nvidia fans) have no measured delay. It is optional: records
written before it existed have no such field, which means "not measured", and they stay valid. The readback delay
is not compared: a different delay doesn't make fan2go analyze the fan again.

Before it loads the saved data, `runInitializationIfNeeded` compares the stored values with the current entry. Data
without a record was measured without an entry, so it counts as `("", 0)`. If they differ, fan2go deletes the RPM
curve, the set→get map, the pwm map and the record, logs it at INFO, and analyzes the fan again. This happens when an
entry with a formula is added, its formula changes (version bump), it is removed, disabled with
`quirks.enabled: false`, or overridden in the config. A read error other than
missing/corrupt data stops the controller, like for the other analysis data. `fan2go fan reset` deletes the record
together with the other data; `fan2go fan init` replaces it together with the other data once the analysis succeeded.

**Why the RPM curve too**: the curve was measured with the readback the old map predicted. A wrong map skips
measurement points, a sweep with failed writes leaves values out of the map (and so out of the pwmMap), and a future
formula may change the key set.

**Trade-off**: after an upgrade that adds an entry with a formula, the fans of that device are analyzed once more,
even if the old data was right (on Aquacomputer devices this takes longer, every PWM read and write waits 200 ms).

## Devices without an entry

Reports that are not backed by a primary source, or that would need more fields, are not in the registry. They are
handled with the regular config:

- **Dell (`dell_smm`)**: the output levels depend on the model (`fan_max`), and the reported PWM changes only when
  the fan state changes, seconds later (#161). Use `sanityCheck.pwmValueChangedByThirdParty.enabled: false` and a
  `pwmMap`/`setPwmToGetPwmMap` with `values` for the states of your model (e.g. 0, 128, 255). On some machines the
  BIOS keeps changing the speed until its control is disabled (e.g. with `dell-bios-fan-control`).
- **AMD GPUs (`amdgpu`)**: the reported PWM may not follow the written value (#148, #196). Use
  `setPwmToGetPwmMap: identity` and disable the `pwmValueChangedByThirdParty` sanity check.
  The zero RPM mode of newer GPUs (`gpu_od/fan_ctrl/fan_zero_rpm_enable`) doesn't apply in manual mode, so the stall
  check (`sanityCheck.fanStalled`) needs no opt-out for it. In the kernel tree the registry is pinned to,
  `pwm1_enable=1` switches off the firmware fan control (`smu_v13_0_set_fan_control_mode` calls
  `smu_v13_0_auto_fan_control(smu, 0)`), and a `pwm1` write programs a static duty cycle into the fan controller
  (`smu_v13_0_set_fan_speed_pwm`, `FDO_PWM_MODE_STATIC`). The zero RPM setting is only a value in the overdrive table
  of the firmware's own fan control (`FanZeroRpmEnable`). Older GPUs (`smu7_thermal.c`) enable zero RPM only when they
  start the firmware fan control, and stop it on a `pwm1` write. SMU 14.0.2 GPUs have no writable `pwm1`.
  Sources: [`smu_v13_0.c` `smu_v13_0_set_fan_speed_pwm`](https://github.com/torvalds/linux/blob/ee83899302a0457a3ba47464a13e8682bd7ba27d/drivers/gpu/drm/amd/pm/swsmu/smu13/smu_v13_0.c#L972),
  [`smu_v13_0_set_fan_control_mode`](https://github.com/torvalds/linux/blob/ee83899302a0457a3ba47464a13e8682bd7ba27d/drivers/gpu/drm/amd/pm/swsmu/smu13/smu_v13_0.c#L1001),
  [`smu7_thermal.c`](https://github.com/torvalds/linux/blob/ee83899302a0457a3ba47464a13e8682bd7ba27d/drivers/gpu/drm/amd/pm/powerplay/hwmgr/smu7_thermal.c#L176), [`amdgpu_pm.c` `pwm1`](https://github.com/torvalds/linux/blob/ee83899302a0457a3ba47464a13e8682bd7ba27d/drivers/gpu/drm/amd/pm/amdgpu_pm.c#L3069).
- **Corsair Commander Pro, fans that don't stop at 0 %** (#316): this depends on the fan. Set `minPwm` or a `pwmMap`
  so that stopping writes a value that the fan reacts to.
- **Out-of-tree NCT6687D driver** (`nct6687`): older versions cached register values for 1 s (#272, #358). The
  current driver reads the PWM register back right after a write, and the mainline `nct6683` driver uses the same
  hwmon name. fan2go measures the readback delay during the analysis and waits for it (see
  [Fan analysis](fan-analysis.md)). If you still see stale values, set `pwmSetDelay` and `setPwmToGetPwmMap: identity`.
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
- An entry applies to all channels of a device. There are no channel-specific entries. Only the `it87` behaviour
  depends on the channel (`FullDutyReadsAsFullSpeedFromChannel`); its hint applies to all channels.
- `thinkpad`: no readback formula, because it differs between models with the same hwmon name; the set/get sweep
  still runs (it is fast on the EC).
- No DMI matching, so model-specific behaviour (e.g. `dell_smm` levels) can't be expressed.
- `corsaircpro`: `pwm` can't be read before fan2go wrote it (kernel docs), so the original PWM isn't restored on exit
  (the device has no automatic mode anyway), and the `pwmValueChangedByThirdParty` check switches itself off on the
  first cycle.
- The `pwmMap` of fans analyzed before 1.0.0 is kept: it is the identity, not one value per output level, until the fan
  is analyzed again (`fan init`). A change of the `pwmMap` derivation doesn't bump entry versions, because the saved
  data stays valid.
