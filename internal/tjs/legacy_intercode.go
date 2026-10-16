package tjs

import (
	"fmt"
	"sync"
)

// LegacyInstruction is one instruction in the original TJS2 intercode stream.
// Offset and operands are measured in 16-bit code units, exactly as serialized.
type LegacyInstruction struct {
	Offset   int
	Opcode   int16
	Name     string
	Operands []int16
}

var legacyOpcodeNames = map[int16]string{
	0: "NOP", 1: "CONST", 2: "CP", 3: "CL", 4: "CCL", 5: "TT", 6: "TF", 7: "CEQ", 8: "CDEQ", 9: "CLT", 10: "CGT", 11: "SETF", 12: "SETNF", 13: "LNOT", 14: "NF", 15: "JF", 16: "JNF", 17: "JMP",
	18: "INC", 19: "INCPD", 20: "INCPI", 21: "INCP", 22: "DEC", 23: "DECPD", 24: "DECPI", 25: "DECP", 26: "LOR", 27: "LORPD", 28: "LORPI", 29: "LORP", 30: "LAND", 31: "LANDPD", 32: "LANDPI", 33: "LANDP", 34: "BOR", 35: "BORPD", 36: "BORPI", 37: "BORP", 38: "BXOR", 39: "BXORPD", 40: "BXORPI", 41: "BXORP", 42: "BAND", 43: "BANDPD", 44: "BANDPI", 45: "BANDP", 46: "SAR", 47: "SARPD", 48: "SARPI", 49: "SARP", 50: "SAL", 51: "SALPD", 52: "SALPI", 53: "SALP", 54: "SR", 55: "SRPD", 56: "SRPI", 57: "SRP", 58: "ADD", 59: "ADDPD", 60: "ADDPI", 61: "ADDP", 62: "SUB", 63: "SUBPD", 64: "SUBPI", 65: "SUBP", 66: "MOD", 67: "MODPD", 68: "MODPI", 69: "MODP", 70: "DIV", 71: "DIVPD", 72: "DIVPI", 73: "DIVP", 74: "IDIV", 75: "IDIVPD", 76: "IDIVPI", 77: "IDIVP", 78: "MUL", 79: "MULPD", 80: "MULPI", 81: "MULP",
	82: "BNOT", 83: "TYPEOF", 84: "TYPEOFD", 85: "TYPEOFI", 86: "EVAL", 87: "EEXP", 88: "CHKINS", 89: "ASC", 90: "CHR", 91: "NUM", 92: "CHS", 93: "INV", 94: "CHKINV", 95: "INT", 96: "REAL", 97: "STR", 98: "OCTET", 99: "CALL", 100: "CALLD", 101: "CALLI", 102: "NEW", 103: "GPD", 104: "SPD", 105: "SPDE", 106: "SPDEH", 107: "GPI", 108: "SPI", 109: "SPIE", 110: "GPDS", 111: "SPDS", 112: "GPIS", 113: "SPIS", 114: "SETP", 115: "GETP", 116: "DELD", 117: "DELI", 118: "SRV", 119: "RET", 120: "ENTRY", 121: "EXTRY", 122: "THROW", 123: "CHGTHIS", 124: "GLOBAL", 125: "ADDCI", 126: "REGMEMBER", 127: "DEBUGGER",
}

type legacyPreparation struct {
	once         sync.Once
	instructions []LegacyInstruction
	offsetToIP   map[int]int
	err          error
}

func (d *DecodedBytecode) preparedLegacyInstructions(object int) ([]LegacyInstruction, map[int]int, error) {
	if object < 0 || object >= len(d.Objects) {
		return nil, nil, fmt.Errorf("legacy intercode object %d: index out of range", object)
	}
	if len(d.legacyPrepared) != len(d.Objects) {
		instructions, err := d.decodeLegacyInstructions(object)
		if err != nil {
			return nil, nil, err
		}
		offsetToIP := make(map[int]int, len(instructions)+1)
		for i, instruction := range instructions {
			offsetToIP[instruction.Offset] = i
		}
		offsetToIP[len(d.Objects[object].Code)] = len(instructions)
		return instructions, offsetToIP, nil
	}
	prepared := &d.legacyPrepared[object]
	prepared.once.Do(func() {
		prepared.instructions, prepared.err = d.decodeLegacyInstructions(object)
		if prepared.err != nil {
			return
		}
		prepared.offsetToIP = make(map[int]int, len(prepared.instructions)+1)
		for i, instruction := range prepared.instructions {
			prepared.offsetToIP[instruction.Offset] = i
		}
		prepared.offsetToIP[len(d.Objects[object].Code)] = len(prepared.instructions)
	})
	return prepared.instructions, prepared.offsetToIP, prepared.err
}

// DecodeLegacyInstructions returns an owned copy of the prepared instructions.
func (d *DecodedBytecode) DecodeLegacyInstructions(object int) ([]LegacyInstruction, error) {
	instructions, _, err := d.preparedLegacyInstructions(object)
	if err != nil {
		return nil, err
	}
	result := make([]LegacyInstruction, len(instructions))
	for i, instruction := range instructions {
		result[i] = instruction
		result[i].Operands = append([]int16(nil), instruction.Operands...)
	}
	return result, nil
}

// decodeLegacyInstructions parses instruction boundaries without executing them.
// Unknown instructions and malformed operands report the owning object and offset.
func (d *DecodedBytecode) decodeLegacyInstructions(object int) ([]LegacyInstruction, error) {
	if object < 0 || object >= len(d.Objects) {
		return nil, fmt.Errorf("legacy intercode object %d: index out of range", object)
	}
	code := d.Objects[object].Code
	result := make([]LegacyInstruction, 0, len(code)/2)
	for pc := 0; pc < len(code); {
		op := code[pc]
		name, ok := legacyOpcodeNames[op]
		if !ok {
			return nil, fmt.Errorf("legacy intercode object %d code offset %d: unknown opcode %d", object, pc, op)
		}
		n := 1
		switch op {
		case 0, 14, 119, 121, 126, 127:
			n = 1
		case 1, 2, 4, 7, 8, 9, 10, 88, 114, 115:
			n = 3
		case 3, 5, 6, 11, 12, 13, 82, 83, 86, 87, 89, 90, 91, 92, 93, 94, 95, 96, 97, 98, 118:
			n = 2
		case 15, 16, 17:
			n = 2
		case 18, 22:
			n = 2
		case 19, 20, 23, 24:
			// INCPD/INCPI/DECPD/DECPI: result, target, key/name constant.
			n = 4
		case 21, 25:
			// INCP/DECP: result, target.
			n = 3
		case 26, 30, 34, 38, 42, 46, 50, 54, 58, 62, 66, 70, 74, 78:
			n = 3
		case 27, 28, 31, 32, 35, 36, 39, 40, 43, 44, 47, 48, 51, 52, 55, 56, 59, 60, 63, 64, 67, 68, 71, 72, 75, 76, 79, 80:
			n = 5
		case 29, 33, 37, 41, 45, 49, 53, 57, 61, 65, 69, 73, 77, 81:
			n = 4
		case 84, 85, 103, 104, 105, 106, 107, 108, 109, 110, 111, 112, 113, 116, 117:
			n = 4
		case 120:
			n = 3
		case 122, 124:
			n = 2
		case 123, 125:
			n = 3
		case 99, 100, 101, 102:
			if pc+3 >= len(code) {
				return nil, fmt.Errorf("legacy intercode object %d code offset %d: truncated %s operands", object, pc, name)
			}
			count := int(code[pc+3])
			if op == 100 || op == 101 {
				if pc+4 >= len(code) {
					return nil, fmt.Errorf("legacy intercode object %d code offset %d: truncated %s operands", object, pc, name)
				}
				count = int(code[pc+4])
				n = 5
			} else {
				n = 4
			}
			if count == -1 {
			} else if count == -2 {
				if pc+n >= len(code) {
					return nil, fmt.Errorf("legacy intercode object %d code offset %d: truncated expanded %s operands", object, pc, name)
				}
				count = int(code[pc+n])
				n++
				if count < 0 {
					return nil, fmt.Errorf("legacy intercode object %d code offset %d: invalid %s argument count %d", object, pc, name, count)
				}
				n += count * 2
			} else if count < 0 {
				return nil, fmt.Errorf("legacy intercode object %d code offset %d: invalid %s argument count %d", object, pc, name, count)
			} else {
				n += count
			}
		}
		if n < 1 || n > len(code)-pc {
			return nil, fmt.Errorf("legacy intercode object %d code offset %d: truncated %s instruction", object, pc, name)
		}
		operands := append([]int16(nil), code[pc+1:pc+n]...)
		result = append(result, LegacyInstruction{Offset: pc, Opcode: op, Name: name, Operands: operands})
		pc += n
	}
	return result, nil
}
