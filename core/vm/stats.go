// Copyright 2022 The go-fantom Authors
// This file is part of the go-fantom library.
//
// The go-fantom library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package vm

import (
	"fmt"
	"time"
	"math/big"
	"sync"
)

// VM Micro Dataset for profiling
type VmMicroData struct {
	opCodeFrequency      map[OpCode]big.Int // opcode frequency statistics
	opCodeDuration       map[OpCode]big.Int // accumulated duration of opcodes
	instructionFrequency map[uint64]big.Int // instruction frequency statistics
	stepLengthFrequency  map[int]big.Int // smart contract length frequency

	mx  sync.Mutex                          // mutex to protect micro dataset 
}

// single global data set for all workers
var vmStats VmMicroData

// update statistics
func (d *VmMicroData) UpdateStatistics(opCodeFrequency map[OpCode]uint64, opCodeDuration map[OpCode]time.Duration, instructionFrequency map[uint64]uint64, stepLength int) {
	// get access to dataset 
	d.mx.Lock()

	// update opcode frequency
	for opCode, freq := range opCodeFrequency {
		value := d.opCodeFrequency[opCode]
		value.Add(&value, new(big.Int).SetUint64(uint64(freq)))
		d.opCodeFrequency[opCode] = value
	}

	// update instruction opCodeDuration
	for opCode, duration := range opCodeDuration {
		value := d.opCodeDuration[opCode]
		value.Add(&value, new(big.Int).SetUint64(uint64(duration)))
		d.opCodeDuration[opCode] = value
	}

	// update instruction frequency
	for instruction, freq := range instructionFrequency {
		value := d.instructionFrequency[instruction]
		value.Add(&value, new(big.Int).SetUint64(uint64(freq)))
		d.instructionFrequency[instruction] = value
	}

	// step length frequency
	value := d.stepLengthFrequency[stepLength]
	value.Add(&value,new(big.Int).SetUint64(uint64(1)))

	// release data set
	d.mx.Unlock()
}

// update statistics
func (d *VmMicroData) PrintStatistics() {
	// get access to dataset 
	d.mx.Lock()

	// print opcode frequency
	for opCode, freq := range d.opCodeFrequency {
		fmt.Printf("opcode-freq: %v,%v", opCode, freq.String())
	}

	// print total opcode duration in seconds
	for opCode, duration := range d.opCodeDuration {
		seconds := new(big.Int)
		seconds.Div(duration, big.newInt(int64(1000000000)))
		fmt.Printf("opcode-total: %v,%v", opCodeToString[opCode], seconds.String())
	}

	// print instruction frequency
	for instruction, freq  := range d.instructionFrequency {
		fmt.Printf("instruction-freq: %v,%v", instruction, freq.String())
	}

	// print step-length frequency
	for length, freq := range d.stepLengthFrequency {
		fmt.Printf("steplen-freq: %v,%v", length, freq.String())
	}

	// release data set
	d.mx.Unlock()
}
