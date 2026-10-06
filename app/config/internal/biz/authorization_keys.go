package biz

import "strings"

// ConfigWriteOperation classifies the actual persisted key. Unknown keys and
// namespaces require the security capability; generic option.update cannot
// introduce a new credential or alter authorization settings.
func ConfigWriteOperation(namespace, key string) string {
	if namespace != "system" {
		return "system.option.security.update"
	}
	switch key {
	case "notice":
		return "system.content.notice.update"
	case "about":
		return "system.content.about.update"
	case "home_page_content":
		return "system.content.home.update"
	case "SystemName", "system_name", "Logo", "logo", "Footer", "footer", "theme", "Theme":
		return "system.option.update"
	}
	lowered := strings.ToLower(key)
	if strings.Contains(lowered, "alipay") || strings.Contains(lowered, "stripe") || strings.Contains(lowered, "payment") || strings.Contains(lowered, "epay") || strings.Contains(lowered, "topup") {
		return "system.option.payment.update"
	}
	if strings.Contains(lowered, "ratio") || strings.Contains(lowered, "price") || strings.Contains(lowered, "quota_per_unit") {
		return "system.option.pricing.update"
	}
	return "system.option.security.update"
}

func configView(entry *ConfigEntry) *ConfigEntry {
	if entry == nil {
		return nil
	}
	copy := *entry
	key := strings.ToLower(entry.Key)
	if ConfigWriteOperation(entry.Namespace, entry.Key) == "system.option.security.update" || strings.Contains(key, "key") || strings.Contains(key, "secret") || strings.Contains(key, "password") || strings.Contains(key, "token") || strings.Contains(key, "privatekey") || strings.Contains(key, "webhook") {
		copy.Value = ""
	}
	return &copy
}
