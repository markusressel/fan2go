/*
 * fan2go
 * Copyright (c) 2019. Markus Ressel
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as
 * published by the Free Software Foundation, either version 3 of the
 * License, or (at ydour option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program.  If not, see <https://www.gnu.org/licenses/>.
 */
package main

import (
	"github.com/markusressel/fan2go/cmd"
	"github.com/markusressel/fan2go/internal/nvidia_base"
)

func main() {
	// the following is needed to make sure the nvml-lib is shutdown correctly
	// it will do nothing if it that lib hasn't been initialized in the first place
	// (or initialization failed, e.g. because no nvidia driver is installed)
	defer nvidia_base.CleanupAtExit()
	cmd.Execute()
}
