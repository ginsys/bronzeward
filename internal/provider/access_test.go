package provider

// kvRead is a KV v2 read answer for one version.
func kvRead(fields map[string]any, metadata map[string]any) map[string]any {
	return data(map[string]any{"data": fields, "metadata": metadata})
}

func kvMetadata(version any, created any) map[string]any {
	return map[string]any{"version": version, "created_time": created, "deletion_time": "", "destroyed": false, "custom_metadata": nil}
}
