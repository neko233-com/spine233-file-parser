package spineparser

import "testing"

func TestDiscoverProjectDeformTimelineReadsMeshOwnerReference(t *testing.T) {
	payload := append([]byte(nil), projectTimelinePrefix...)
	payload = append(payload, 0x06, 0x01, 0x01)
	payload = append(payload, projectTimelineKeyPrefix...)
	payload = appendFloat32ForTest(payload, 0)
	payload = append(payload, 0x00, 0x01, 0x02, 0x00)
	payload = append(
		payload,
		ProjectAttachmentClassMesh,
		0xe6,
		0x01,
		0x01,
		0x07,
		0x04,
		0x00,
	)
	timelines := discoverProjectDeformTimelinesInGroupV2(
		payload,
		0,
		len(payload),
	)
	if len(timelines) != 1 ||
		timelines[0].AttachmentClassID != ProjectAttachmentClassMesh ||
		timelines[0].AttachmentReference != 230 ||
		timelines[0].VertexCount != 2 ||
		len(timelines[0].Keys) != 1 {
		t.Fatalf("timelines = %#v", timelines)
	}
}

func TestReadProjectDeformKeysReadsDenseFinalChunk(t *testing.T) {
	payload := appendProjectDeformKeyHeaderForTest(nil, 0, 0)
	payload = append(payload, 0x01)
	payload = appendPositiveVarintForTest(payload, 576)
	payload = appendPositiveVarintForTest(payload, 5)
	payload = append(payload, 0, 0, 0, 0, 2)
	for index := 0; index < 64; index++ {
		value := float32(0)
		if index == 0 {
			value = 12.5
		} else if index == 1 {
			value = -3.25
		}
		payload = appendFloat32ForTest(payload, value)
	}

	keys, vertexCount, cursor, ok := readProjectDeformKeysV2(
		payload,
		0,
		len(payload),
		1,
	)
	if !ok || cursor != len(payload) || vertexCount != 576 ||
		len(keys) != 1 || !keys[0].HasVertices ||
		len(keys[0].Vertices) != 576 ||
		keys[0].Vertices[512] != 12.5 ||
		keys[0].Vertices[513] != -3.25 {
		t.Fatalf(
			"keys = %#v, vertexCount = %d, cursor = %d, ok = %v",
			keys,
			vertexCount,
			cursor,
			ok,
		)
	}
}

func TestReadProjectDeformKeysReadsFullNonZeroChunkCountByte(t *testing.T) {
	payload := appendProjectDeformKeyHeaderForTest(nil, 0, 0)
	payload = append(payload, 0x01)
	payload = appendPositiveVarintForTest(payload, 128)
	payload = appendPositiveVarintForTest(payload, 1)
	payload = append(payload, 0x80)
	for index := 0; index < 128; index++ {
		payload = appendFloat32ForTest(payload, float32(index+1))
	}

	keys, vertexCount, cursor, ok := readProjectChunkedDeformKeysV2(
		payload,
		0,
		len(payload),
		1,
	)
	if !ok || cursor != len(payload) || vertexCount != 128 ||
		len(keys) != 1 || len(keys[0].Vertices) != 128 ||
		keys[0].Vertices[127] != 128 {
		t.Fatalf(
			"keys = %#v, vertexCount = %d, cursor = %d, ok = %v",
			keys,
			vertexCount,
			cursor,
			ok,
		)
	}
}

func TestReadProjectDeformKeysRejectsChunkNonZeroCountMismatch(t *testing.T) {
	payload := appendProjectDeformKeyHeaderForTest(nil, 0, 0)
	payload = append(payload, 0x01)
	payload = appendPositiveVarintForTest(payload, 2)
	payload = appendPositiveVarintForTest(payload, 1)
	payload = appendPositiveVarintForTest(payload, 2)
	payload = appendFloat32ForTest(payload, 1)
	payload = appendFloat32ForTest(payload, 0)

	if _, _, _, ok := readProjectChunkedDeformKeysV2(
		payload,
		0,
		len(payload),
		1,
	); ok {
		t.Fatal("invalid non-zero count accepted")
	}
}

func TestReadProjectDeformKeysReadsLegacyFloatArrayChunks(t *testing.T) {
	payload := appendProjectDeformKeyHeaderForTest(nil, 0, 0)
	payload = append(payload, 0x01)
	payload = appendPositiveVarintForTest(payload, 236)
	payload = append(payload, 0x02, 0x80)
	for index := 0; index < 128; index++ {
		payload = appendFloat32ForTest(payload, float32(index+1))
	}
	payload = append(payload, 108)
	for index := 0; index < 108; index++ {
		payload = appendFloat32ForTest(payload, float32(index+129))
	}

	keys, vertexCount, cursor, ok := readProjectDeformKeysV2(
		payload,
		0,
		len(payload),
		1,
	)
	if !ok || cursor != len(payload) || vertexCount != 236 ||
		len(keys) != 1 || !keys[0].HasVertices ||
		len(keys[0].Vertices) != 236 ||
		keys[0].Vertices[127] != 128 ||
		keys[0].Vertices[128] != 129 ||
		keys[0].Vertices[235] != 236 {
		t.Fatalf(
			"keys = %#v, vertexCount = %d, cursor = %d, ok = %v",
			keys,
			vertexCount,
			cursor,
			ok,
		)
	}
}

func TestReadProjectDeformKeysReadsSingleSteppedKey(t *testing.T) {
	payload := appendProjectDeformKeyHeaderForTest(nil, 15, 2)
	payload = append(payload, 0x01, 0x02, 0x00)

	keys, vertexCount, cursor, ok := readProjectDeformKeysV2(
		payload,
		0,
		len(payload),
		1,
	)
	if !ok || cursor != len(payload) || vertexCount != 2 ||
		len(keys) != 1 || keys[0].CurveFlag != 2 ||
		keys[0].Time != 0.5 {
		t.Fatalf(
			"keys = %#v, vertexCount = %d, cursor = %d, ok = %v",
			keys,
			vertexCount,
			cursor,
			ok,
		)
	}
}

func TestReadProjectDeformKeysReadsTwoSteppedKeys(t *testing.T) {
	payload := appendProjectDeformKeyHeaderForTest(nil, 0, 2)
	payload = append(payload, 0x01, 0x02, 0x00)
	payload = appendProjectDeformKeyHeaderForTest(payload, 30, 2)
	payload = append(payload, 0x01, 0x02, 0x00)

	keys, vertexCount, cursor, ok := readProjectDeformKeysV2(
		payload,
		0,
		len(payload),
		2,
	)
	if !ok || cursor != len(payload) || vertexCount != 2 ||
		len(keys) != 2 || keys[0].CurveFlag != 2 ||
		keys[1].CurveFlag != 2 {
		t.Fatalf(
			"keys = %#v, vertexCount = %d, cursor = %d, ok = %v",
			keys,
			vertexCount,
			cursor,
			ok,
		)
	}
}

func TestReadProjectDeformKeysReadsExpandedCurves(t *testing.T) {
	payload := appendProjectDeformExpandedKeyForTest(
		nil,
		0,
		[5]float32{1, 2, 3, 4, 99},
	)
	payload = appendProjectDeformExpandedKeyForTest(
		payload,
		30,
		[5]float32{5, 6, 7, 8, 100},
	)

	keys, vertexCount, cursor, ok := readProjectDeformKeysV2(
		payload,
		0,
		len(payload),
		2,
	)
	if !ok || cursor != len(payload) || vertexCount != 2 ||
		len(keys) != 2 ||
		keys[0].Curve != [4]float32{1, 2, 3, 4} ||
		keys[1].Curve != [4]float32{5, 6, 7, 8} {
		t.Fatalf(
			"keys = %#v, vertexCount = %d, cursor = %d, ok = %v",
			keys,
			vertexCount,
			cursor,
			ok,
		)
	}
}

func TestResolveProjectVertexDeformReference(t *testing.T) {
	payload := appendPositiveVarintForTest(
		nil,
		ProjectAttachmentClassPath,
	)
	payload = appendPositiveVarintForTest(payload, 2211)
	payload = append(payload, projectTimelinePrefix...)
	attachments := &ProjectVertexAttachmentDirectory{
		Records: []ProjectVertexAttachmentRecord{
			{
				ClassID:       ProjectAttachmentClassPath,
				WireReference: 2211,
				Name:          "glow_lujin",
			},
		},
	}
	attachment, ok := resolveProjectVertexDeformReference(
		payload,
		0,
		len(payload),
		attachments,
	)
	if !ok || attachment.WireReference != 2211 ||
		attachment.Name != "glow_lujin" {
		t.Fatalf("attachment = %#v, ok = %t", attachment, ok)
	}
}

func appendProjectDeformKeyHeaderForTest(
	output []byte,
	frame float32,
	curveFlag byte,
) []byte {
	output = append(output, projectTimelineKeyPrefix...)
	output = appendFloat32ForTest(output, frame)
	return append(output, curveFlag)
}

func appendProjectDeformExpandedKeyForTest(
	output []byte,
	frame float32,
	curve [5]float32,
) []byte {
	output = appendProjectDeformKeyHeaderForTest(output, frame, 1)
	for _, value := range curve {
		output = appendFloat32ForTest(output, value)
	}
	return append(output, 0x01, 0x02, 0x00)
}
