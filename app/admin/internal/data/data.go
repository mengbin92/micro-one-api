package data

import "gorm.io/gorm"

// Data aggregates long-lived storage clients for admin-api.
type Data struct{ DB *gorm.DB }
