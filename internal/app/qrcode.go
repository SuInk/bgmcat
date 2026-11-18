package app

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

type qrVersionSpec struct {
	version  int
	totalCW  int
	ecPerBlk int
	g1Blocks int
	g1DataCW int
	g2Blocks int
	g2DataCW int
	charBits int
	alignPos []int
}

var qrVersionsL = []qrVersionSpec{
	{version: 1, totalCW: 26, ecPerBlk: 7, g1Blocks: 1, g1DataCW: 19, charBits: 8},
	{version: 2, totalCW: 44, ecPerBlk: 10, g1Blocks: 1, g1DataCW: 34, charBits: 8, alignPos: []int{6, 18}},
	{version: 3, totalCW: 70, ecPerBlk: 15, g1Blocks: 1, g1DataCW: 55, charBits: 8, alignPos: []int{6, 22}},
	{version: 4, totalCW: 100, ecPerBlk: 20, g1Blocks: 1, g1DataCW: 80, charBits: 8, alignPos: []int{6, 26}},
	{version: 5, totalCW: 134, ecPerBlk: 26, g1Blocks: 1, g1DataCW: 108, charBits: 8, alignPos: []int{6, 30}},
	{version: 6, totalCW: 172, ecPerBlk: 18, g1Blocks: 2, g1DataCW: 68, charBits: 8, alignPos: []int{6, 34}},
	{version: 7, totalCW: 196, ecPerBlk: 20, g1Blocks: 2, g1DataCW: 78, charBits: 8, alignPos: []int{6, 22, 38}},
	{version: 8, totalCW: 242, ecPerBlk: 24, g1Blocks: 2, g1DataCW: 97, charBits: 8, alignPos: []int{6, 24, 42}},
	{version: 9, totalCW: 292, ecPerBlk: 30, g1Blocks: 2, g1DataCW: 116, charBits: 8, alignPos: []int{6, 26, 46}},
	{version: 10, totalCW: 346, ecPerBlk: 18, g1Blocks: 2, g1DataCW: 68, g2Blocks: 2, g2DataCW: 69, charBits: 16, alignPos: []int{6, 28, 50}},
}

var (
	qrGFExp [512]byte
	qrGFLog [256]byte
)

func init() {
	x := 1
	for i := 0; i < 255; i++ {
		qrGFExp[i] = byte(x)
		qrGFExp[i+255] = byte(x)
		qrGFLog[x] = byte(i)
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11d
		}
	}
	qrGFExp[510] = qrGFExp[0]
	qrGFExp[511] = qrGFExp[1]
}

func qrGFMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return qrGFExp[int(qrGFLog[a])+int(qrGFLog[b])]
}

func qrRSGeneratorPoly(deg int) []byte {
	poly := []byte{1}
	for i := 0; i < deg; i++ {
		next := make([]byte, len(poly)+1)
		factor := qrGFExp[i]
		for j, p := range poly {
			next[j] ^= p
			next[j+1] ^= qrGFMul(p, factor)
		}
		poly = next
	}
	return poly[1:]
}

func qrRSRemainder(data []byte, deg int) []byte {
	gen := qrRSGeneratorPoly(deg)
	rem := make([]byte, deg)
	for _, b := range data {
		factor := b ^ rem[0]
		copy(rem, rem[1:])
		rem[deg-1] = 0
		if factor != 0 {
			for j := 0; j < deg; j++ {
				rem[j] ^= qrGFMul(gen[j], factor)
			}
		}
	}
	return rem
}

type qrBitBuffer struct {
	bits []bool
}

func (b *qrBitBuffer) appendBits(val, count int) {
	for i := count - 1; i >= 0; i-- {
		b.bits = append(b.bits, ((val>>i)&1) == 1)
	}
}

func chooseQRVersionL(payload []byte) (qrVersionSpec, error) {
	n := len(payload)
	for _, spec := range qrVersionsL {
		dataCW := spec.g1Blocks*spec.g1DataCW + spec.g2Blocks*spec.g2DataCW
		capBits := dataCW * 8
		needed := 4 + spec.charBits + n*8
		if needed <= capBits {
			return spec, nil
		}
	}
	return qrVersionSpec{}, errors.New("二维码内容过长（最多支持 250 字节）")
}

func encodeQRCodeCodewords(payload []byte, spec qrVersionSpec) []byte {
	dataCW := spec.g1Blocks*spec.g1DataCW + spec.g2Blocks*spec.g2DataCW
	capBits := dataCW * 8
	var bb qrBitBuffer
	bb.appendBits(0b0100, 4) // Byte mode
	bb.appendBits(len(payload), spec.charBits)
	for _, ch := range payload {
		bb.appendBits(int(ch), 8)
	}
	term := min(4, capBits-len(bb.bits))
	if term > 0 {
		bb.appendBits(0, term)
	}
	for len(bb.bits)%8 != 0 {
		bb.bits = append(bb.bits, false)
	}
	dataBytes := make([]byte, 0, dataCW)
	for i := 0; i < len(bb.bits); i += 8 {
		var v byte
		for j := 0; j < 8; j++ {
			if bb.bits[i+j] {
				v |= 1 << (7 - j)
			}
		}
		dataBytes = append(dataBytes, v)
	}
	padBytes := [2]byte{0xEC, 0x11}
	for i := 0; len(dataBytes) < dataCW; i++ {
		dataBytes = append(dataBytes, padBytes[i&1])
	}

	totalBlocks := spec.g1Blocks + spec.g2Blocks
	dataBlocks := make([][]byte, totalBlocks)
	ecBlocks := make([][]byte, totalBlocks)
	offset := 0
	maxDataLen := 0
	for b := 0; b < totalBlocks; b++ {
		blkLen := spec.g1DataCW
		if b >= spec.g1Blocks {
			blkLen = spec.g2DataCW
		}
		blk := append([]byte(nil), dataBytes[offset:offset+blkLen]...)
		offset += blkLen
		dataBlocks[b] = blk
		ecBlocks[b] = qrRSRemainder(blk, spec.ecPerBlk)
		if blkLen > maxDataLen {
			maxDataLen = blkLen
		}
	}

	out := make([]byte, 0, spec.totalCW)
	for col := 0; col < maxDataLen; col++ {
		for b := 0; b < totalBlocks; b++ {
			if col < len(dataBlocks[b]) {
				out = append(out, dataBlocks[b][col])
			}
		}
	}
	for col := 0; col < spec.ecPerBlk; col++ {
		for b := 0; b < totalBlocks; b++ {
			out = append(out, ecBlocks[b][col])
		}
	}
	return out
}

func newQRMatrix(size int) ([][]bool, [][]bool) {
	m := make([][]bool, size)
	fn := make([][]bool, size)
	for i := 0; i < size; i++ {
		m[i] = make([]bool, size)
		fn[i] = make([]bool, size)
	}
	return m, fn
}

func placeQRFinder(m, fn [][]bool, topR, topC int) {
	size := len(m)
	for dr := -1; dr <= 7; dr++ {
		r := topR + dr
		if r < 0 || r >= size {
			continue
		}
		for dc := -1; dc <= 7; dc++ {
			c := topC + dc
			if c < 0 || c >= size {
				continue
			}
			fn[r][c] = true
			if dr == -1 || dr == 7 || dc == -1 || dc == 7 {
				m[r][c] = false
				continue
			}
			isBorder := dr == 0 || dr == 6 || dc == 0 || dc == 6
			isCenter := dr >= 2 && dr <= 4 && dc >= 2 && dc <= 4
			m[r][c] = isBorder || isCenter
		}
	}
}

func placeQRAlignment(m, fn [][]bool, centerR, centerC int) {
	for dr := -2; dr <= 2; dr++ {
		for dc := -2; dc <= 2; dc++ {
			r, c := centerR+dr, centerC+dc
			fn[r][c] = true
			m[r][c] = dr == -2 || dr == 2 || dc == -2 || dc == 2 || (dr == 0 && dc == 0)
		}
	}
}

func drawQRFormatBits(m, fn [][]bool, mask int) {
	size := len(m)
	data := (0b01 << 3) | (mask & 0x7) // ECC Level L = 01
	d := data << 10
	for i := 14; i >= 10; i-- {
		if (d>>i)&1 != 0 {
			d ^= 0x537 << (i - 10)
		}
	}
	bits := ((data << 10) | d) ^ 0x5412
	for i := 0; i <= 14; i++ {
		val := ((bits >> i) & 1) == 1
		// Copy 1 around top-left finder
		switch {
		case i < 6:
			m[i][8], fn[i][8] = val, true
		case i < 8:
			m[i+1][8], fn[i+1][8] = val, true
		case i == 8:
			m[8][7], fn[8][7] = val, true
		default:
			m[8][14-i], fn[8][14-i] = val, true
		}
		// Copy 2 along bottom-left & top-right
		if i < 8 {
			m[8][size-1-i], fn[8][size-1-i] = val, true
		} else {
			m[size-15+i][8], fn[size-15+i][8] = val, true
		}
	}
}

func drawQRVersionBits(m, fn [][]bool, version int) {
	if version < 7 {
		return
	}
	size := len(m)
	d := version << 12
	for i := 17; i >= 12; i-- {
		if (d>>i)&1 != 0 {
			d ^= 0x1f25 << (i - 12)
		}
	}
	bits := (version << 12) | d
	for i := 0; i < 18; i++ {
		val := ((bits >> i) & 1) == 1
		a := size - 11 + (i % 3)
		b := i / 3
		m[b][a], fn[b][a] = val, true
		m[a][b], fn[a][b] = val, true
	}
}

func qrMaskBit(mask, r, c int) bool {
	switch mask {
	case 0:
		return (r+c)%2 == 0
	case 1:
		return r%2 == 0
	case 2:
		return c%3 == 0
	case 3:
		return (r+c)%3 == 0
	case 4:
		return ((r/2)+(c/3))%2 == 0
	case 5:
		return ((r*c)%2)+((r*c)%3) == 0
	case 6:
		return (((r*c)%2)+((r*c)%3))%2 == 0
	default:
		return (((r+c)%2)+((r*c)%3))%2 == 0
	}
}

func qrPenaltyScore(m [][]bool) int {
	size := len(m)
	score := 0

	// Rule 1: adjacent modules in row/column in same color (>= 5)
	for r := 0; r < size; r++ {
		runH, runV := 1, 1
		for c := 1; c < size; c++ {
			if m[r][c] == m[r][c-1] {
				runH++
				if runH == 5 {
					score += 3
				} else if runH > 5 {
					score++
				}
			} else {
				runH = 1
			}
			if m[c][r] == m[c-1][r] {
				runV++
				if runV == 5 {
					score += 3
				} else if runV > 5 {
					score++
				}
			} else {
				runV = 1
			}
		}
	}

	// Rule 2: 2x2 blocks of same color
	for r := 0; r < size-1; r++ {
		for c := 0; c < size-1; c++ {
			v := m[r][c]
			if m[r][c+1] == v && m[r+1][c] == v && m[r+1][c+1] == v {
				score += 3
			}
		}
	}

	// Rule 3: finder-like 1:1:3:1:1 patterns with 4-module light padding
	matchRun := func(get func(int) bool) {
		for i := 0; i <= size-11; i++ {
			p1 := get(i) && !get(i+1) && get(i+2) && get(i+3) && get(i+4) && !get(i+5) && get(i+6) && !get(i+7) && !get(i+8) && !get(i+9) && !get(i+10)
			p2 := !get(i) && !get(i+1) && !get(i+2) && !get(i+3) && get(i+4) && !get(i+5) && get(i+6) && get(i+7) && get(i+8) && !get(i+9) && get(i+10)
			if p1 || p2 {
				score += 40
			}
		}
	}
	for idx := 0; idx < size; idx++ {
		r := idx
		matchRun(func(c int) bool { return m[r][c] })
		matchRun(func(rr int) bool { return m[rr][r] })
	}

	// Rule 4: dark/total ratio deviation from 50%
	dark := 0
	for r := 0; r < size; r++ {
		for c := 0; c < size; c++ {
			if m[r][c] {
				dark++
			}
		}
	}
	total := size * size
	k := (dark*100)/total - 50
	if k < 0 {
		k = -k
	}
	score += (k / 5) * 10
	return score
}

func encodeQRMatrix(text string) ([][]bool, error) {
	payload := []byte(text)
	if len(payload) == 0 {
		return nil, errors.New("二维码内容不能为空")
	}
	if len(payload) > 250 {
		return nil, errors.New("二维码内容过长（最多 250 字节）")
	}
	spec, err := chooseQRVersionL(payload)
	if err != nil {
		return nil, err
	}
	codewords := encodeQRCodeCodewords(payload, spec)
	size := 17 + 4*spec.version
	base, fn := newQRMatrix(size)

	placeQRFinder(base, fn, 0, 0)
	placeQRFinder(base, fn, 0, size-7)
	placeQRFinder(base, fn, size-7, 0)

	for i := 8; i < size-8; i++ {
		dark := i%2 == 0
		base[6][i], fn[6][i] = dark, true
		base[i][6], fn[i][6] = dark, true
	}

	if len(spec.alignPos) > 0 {
		for _, r := range spec.alignPos {
			for _, c := range spec.alignPos {
				if (r <= 8 && c <= 8) || (r <= 8 && c >= size-9) || (r >= size-9 && c <= 8) {
					continue
				}
				placeQRAlignment(base, fn, r, c)
			}
		}
	}

	// Dark module + reserve format/version areas so zig-zag data skips them.
	base[size-8][8], fn[size-8][8] = true, true
	drawQRFormatBits(base, fn, 0)
	drawQRVersionBits(base, fn, spec.version)

	bits := make([]bool, 0, len(codewords)*8)
	for _, cw := range codewords {
		for b := 7; b >= 0; b-- {
			bits = append(bits, ((cw>>b)&1) == 1)
		}
	}
	bitIdx := 0
	up := true
	for right := size - 1; right > 0; right -= 2 {
		if right == 6 {
			right--
		}
		for step := 0; step < size; step++ {
			r := step
			if up {
				r = size - 1 - step
			}
			for dx := 0; dx < 2; dx++ {
				c := right - dx
				if fn[r][c] {
					continue
				}
				if bitIdx < len(bits) {
					base[r][c] = bits[bitIdx]
					bitIdx++
				}
			}
		}
		up = !up
	}

	var best [][]bool
	bestScore := int(^uint(0) >> 1)
	for mask := 0; mask < 8; mask++ {
		cand, candFn := newQRMatrix(size)
		for r := 0; r < size; r++ {
			copy(candFn[r], fn[r])
			for c := 0; c < size; c++ {
				v := base[r][c]
				if !fn[r][c] && qrMaskBit(mask, r, c) {
					v = !v
				}
				cand[r][c] = v
			}
		}
		drawQRFormatBits(cand, candFn, mask)
		if score := qrPenaltyScore(cand); score < bestScore {
			bestScore = score
			best = cand
		}
	}
	return best, nil
}

func encodeQRSVG(text string) (string, error) {
	matrix, err := encodeQRMatrix(text)
	if err != nil {
		return "", err
	}
	n := len(matrix)
	const quiet = 4
	total := n + quiet*2
	var path strings.Builder
	for r := 0; r < n; r++ {
		for c := 0; c < n; c++ {
			if matrix[r][c] {
				fmt.Fprintf(&path, "M%d,%dh1v1h-1z", c+quiet, r+quiet)
			}
		}
	}
	return fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges" role="img" aria-label="QR Code"><rect width="%d" height="%d" fill="#ffffff"/><path fill="#111827" d="%s"/></svg>`,
		total, total, total, total, path.String(),
	), nil
}

func (a *App) remoteRoutes() {
	a.mux.HandleFunc("GET /api/remote/qrcode", func(w http.ResponseWriter, r *http.Request) {
		text := strings.TrimSpace(r.URL.Query().Get("text"))
		if text == "" {
			fail(w, http.StatusBadRequest, "请提供二维码内容")
			return
		}
		svg, err := encodeQRSVG(text)
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(svg))
	})
}
