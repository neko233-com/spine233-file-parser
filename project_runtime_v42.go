package spineparser

// discoverProjectRuntimeModelV42 是 4.2 私有布局的独立入口。
// 4.2 字段表尚未与 4.3 对象表混用；缺少对应解码器时必须明确失败，不能误把 4.2 数据当 4.3 猜测。
func discoverProjectRuntimeModelV42(payload []byte, sourceVersion string) (*ProjectRuntimeModel, error) {
	return discoverLegacyProjectRuntimeModel(payload, sourceVersion, "spine-4.2-project")
}
