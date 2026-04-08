package camera

type miHomeVideoBootstrapState struct {
	started bool
	h264SPS []byte
	h264PPS []byte
	h265VPS []byte
	h265SPS []byte
	h265PPS []byte
}

func (state *miHomeVideoBootstrapState) prepare(codec string, payload []byte) (bool, []byte) {
	nalus := splitAnnexBNALUs(payload)
	if len(nalus) == 0 {
		return false, nil
	}
	switch normalizeCodec(codec) {
	case "h265":
		hasKeyframe := false
		for _, nalu := range nalus {
			if len(nalu) < 2 {
				continue
			}
			naluType := (nalu[0] >> 1) & 0x3f
			switch naluType {
			case 32:
				state.h265VPS = append([]byte(nil), nalu...)
			case 33:
				state.h265SPS = append([]byte(nil), nalu...)
			case 34:
				state.h265PPS = append([]byte(nil), nalu...)
			case 16, 17, 18, 19, 20, 21:
				hasKeyframe = true
			}
		}
		if !state.started {
			if !hasKeyframe || len(state.h265VPS) == 0 || len(state.h265SPS) == 0 || len(state.h265PPS) == 0 {
				return false, nil
			}
			state.started = true
			return true, annexBPayload(append(cloneParameterSets([][]byte{state.h265VPS, state.h265SPS, state.h265PPS}), nalus...))
		}
	default:
		hasKeyframe := false
		for _, nalu := range nalus {
			if len(nalu) == 0 {
				continue
			}
			switch nalu[0] & 0x1f {
			case 7:
				state.h264SPS = append([]byte(nil), nalu...)
			case 8:
				state.h264PPS = append([]byte(nil), nalu...)
			case 5:
				hasKeyframe = true
			}
		}
		if !state.started {
			if !hasKeyframe || len(state.h264SPS) == 0 || len(state.h264PPS) == 0 {
				return false, nil
			}
			state.started = true
			return true, annexBPayload(append(cloneParameterSets([][]byte{state.h264SPS, state.h264PPS}), nalus...))
		}
	}
	return true, append([]byte(nil), payload...)
}

func cloneParameterSets(nalus [][]byte) [][]byte {
	filtered := make([][]byte, 0, len(nalus))
	for _, nalu := range nalus {
		if len(nalu) == 0 {
			continue
		}
		filtered = append(filtered, append([]byte(nil), nalu...))
	}
	return filtered
}
