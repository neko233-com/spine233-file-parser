package spineparser

// ProjectSkinAttachmentRecord maps one decoded region attachment to its
// region-backed setup slot.
type ProjectSkinAttachmentRecord struct {
	SlotName      string                        `json:"slotName"`
	BoneName      string                        `json:"boneName"`
	BoneReference int                           `json:"boneReference"`
	Attachment    ProjectRegionAttachmentRecord `json:"attachment"`
}

// ProjectSkinDirectory contains the default skin subset currently proven for
// 4.3.23 region-backed projects.
type ProjectSkinDirectory struct {
	Format      string                        `json:"format"`
	Name        string                        `json:"name"`
	Attachments []ProjectSkinAttachmentRecord `json:"attachments"`
}

// DiscoverProjectDefaultSkin resolves decoded region attachments to setup
// slots. Non-default skins and non-region attachment types remain fail-closed.
func DiscoverProjectDefaultSkin(payload []byte) (*ProjectSkinDirectory, error) {
	bones, err := DiscoverProjectBones(payload)
	if err != nil {
		return nil, err
	}
	regions, err := DiscoverProjectRegionAttachments(payload)
	if err != nil {
		return nil, err
	}
	slots, err := DiscoverProjectSlots(payload)
	if err != nil {
		return nil, err
	}
	if len(bones.Records) == 1 &&
		len(regions.Records) == 1 &&
		len(slots.Records) == 1 {
		return &ProjectSkinDirectory{
			Format: "kryo-single-root-region-v2",
			Name:   "default",
			Attachments: []ProjectSkinAttachmentRecord{
				{
					SlotName:      slots.Records[0].Name,
					BoneName:      bones.Records[0].Name,
					BoneReference: slots.Records[0].BoneReference,
					Attachment:    regions.Records[0],
				},
			},
		}, nil
	}
	references, err := matchProjectRegionsToBones(regions, bones)
	if err != nil {
		return nil, err
	}
	slotByReference := make(map[int]ProjectSlotRecord, len(slots.Records))
	for _, slot := range slots.Records {
		slotByReference[slot.BoneReference] = slot
	}
	attachments := make([]ProjectSkinAttachmentRecord, 0, len(regions.Records))
	for index, region := range regions.Records {
		slot, ok := slotByReference[references[index]]
		if !ok {
			return nil, &ParseError{
				Code: ErrInvalidProject,
				Msg:  "region-backed setup slot is missing",
			}
		}
		attachments = append(attachments, ProjectSkinAttachmentRecord{
			SlotName:      slot.Name,
			BoneName:      slot.BoneName,
			BoneReference: slot.BoneReference,
			Attachment:    region,
		})
	}
	return &ProjectSkinDirectory{
		Format:      "kryo-default-skin-v2",
		Name:        "default",
		Attachments: attachments,
	}, nil
}
