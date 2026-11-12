package quirks

// linuxSource is the kernel source tree the formulas below were checked against.
const linuxSource = "https://github.com/torvalds/linux/blob/ee83899302a0457a3ba47464a13e8682bd7ba27d/"

// registry holds all known entries. Every behaviour of an entry must be backed by a primary source
// (kernel docs or driver source), linked in Sources. See docs/hardware-quirks.md.
var registry = []*Entry{
	{
		ID:          "corsaircpro",
		Version:     1,
		Description: "Corsair Commander Pro / Obsidian 1000D (corsair-cpro)",
		Sources: []string{
			"https://docs.kernel.org/hwmon/corsair-cpro.html",
			linuxSource + "drivers/hwmon/corsair-cpro.c#L201",
			linuxSource + "drivers/hwmon/corsair-cpro.c#L294",
			"https://github.com/markusressel/fan2go/issues/64",
			"https://github.com/markusressel/fan2go/issues/132",
		},
		Match: Match{Names: []string{"corsaircpro"}},
		// set_pwm: percent = DIV_ROUND_CLOSEST(val * 100, 255); read: DIV_ROUND_CLOSEST(percent * 255, 100)
		Readback: func(set int) int {
			return divRoundClosest(divRoundClosest(set*100, 255)*255, 100)
		},
		Advice: []AdviceFunc{adviseReadbackFormula, adviseSmoothingResolution},
	},
	{
		ID:          "aquacomputer",
		Version:     1,
		Description: "Aquacomputer D5 Next / Octo / Quadro / Aquaero (aquacomputer_d5next)",
		Sources: []string{
			"https://docs.kernel.org/hwmon/aquacomputer_d5next.html",
			linuxSource + "drivers/hwmon/aquacomputer_d5next.c#L633",
			"https://github.com/markusressel/fan2go/issues/324",
			"https://github.com/markusressel/fan2go/issues/273",
		},
		// only the devices with writable pwm, which all wait 200 ms between two control reports
		// (CTRL_REPORT_DELAY), so measuring the readback of all 256 values takes minutes
		Match: Match{Names: []string{"d5next", "octo", "quadro", "aquaero"}},
		// pwm is stored in centi-percent: DIV_ROUND_CLOSEST(val * 100 * 100, 255) and read back with
		// DIV_ROUND_CLOSEST(val * 255, 100 * 100), which gives back every written value exactly
		Readback: func(set int) int {
			return divRoundClosest(divRoundClosest(set*100*100, 255)*255, 100*100)
		},
		Advice: []AdviceFunc{adviseReadbackFormula},
	},
	{
		ID:          "thinkpad",
		Version:     1,
		Description: "ThinkPad embedded controller (thinkpad_acpi)",
		Sources: []string{
			"https://docs.kernel.org/admin-guide/laptops/thinkpad-acpi.html",
			linuxSource + "drivers/platform/x86/lenovo/thinkpad_acpi.c#L8700",
			linuxSource + "drivers/platform/x86/lenovo/thinkpad_acpi.c#L8366",
			"https://github.com/markusressel/fan2go/issues/47",
		},
		Match: Match{Names: []string{"thinkpad"}},
		// 8 fan levels: pwm1 writes level = (pwm >> 5) & 0x07. The readback is level * 255 / 7 on most models,
		// but models with the ACPI FANG/FANW methods (e.g. E531) store the level in percent and read back
		// other values, and they can't be told apart by name, so there is no Readback.
		Levels: 8,
		Hint:   "the thinkpad_acpi driver only allows fan control when it is loaded with fan_control=1",
		Advice: []AdviceFunc{adviseSmoothingResolution, adviseThinkpadFanControl},
	},
	{
		ID:          "nzxtsmart2",
		Version:     1,
		Description: "NZXT RGB & Fan Controller / Smart Device v2 (nzxt-smart2)",
		Sources: []string{
			"https://docs.kernel.org/hwmon/nzxt-smart2.html",
			linuxSource + "drivers/hwmon/nzxt-smart2.c#L209",
			linuxSource + "drivers/hwmon/nzxt-smart2.c#L509",
		},
		Match: Match{Names: []string{"nzxtsmart2"}},
		// pwm is sent to the device in percent and read back from the same value:
		// scale_pwm_value(val, 255, 100), then scale_pwm_value(percent, 100, 255)
		Readback: func(set int) int {
			return nzxtSmart2Scale(nzxtSmart2Scale(set, 255, 100), 100, 255)
		},
		// set_pwm_enable accepts only the current value: 1 if the device detected a fan on the channel
		Hint: "the nzxt-smart2 driver only accepts pwm_enable 1 on channels where the device detected a fan " +
			"when the driver was loaded; reload the driver after connecting fans",
		Advice: []AdviceFunc{adviseReadbackFormula, adviseSmoothingResolution, adviseOnlyPwmControlMode},
	},
}

// divRoundClosest is the kernel's DIV_ROUND_CLOSEST for non-negative values.
func divRoundClosest(x int, divisor int) int {
	return (x + divisor/2) / divisor
}

// nzxtSmart2Scale is scale_pwm_value of the nzxt-smart2 driver: positive values never become 0.
func nzxtSmart2Scale(val int, origMax int, newMax int) int {
	if val <= 0 {
		return 0
	}
	return max(1, divRoundClosest(min(val, origMax)*newMax, origMax))
}
