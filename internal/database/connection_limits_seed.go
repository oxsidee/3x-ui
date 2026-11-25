package database

import (
	"encoding/json"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

const clientLimitsToConnectionsSeeder = "ClientLimitsMovedToConnections"

func seedClearClientLevelLimits() error {
	var count int64
	if err := db.Model(&model.HistoryOfSeeders{}).Where("seeder_name = ?", clientLimitsToConnectionsSeeder).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.ClientRecord{}).Where("1 = 1").Updates(map[string]any{
			"total_gb": 0, "limit_ip": 0, "limit_hwid": 0, "expiry_time": 0,
		}).Error; err != nil {
			return err
		}
		if err := tx.Model(&xray.ClientTraffic{}).Where("1 = 1").Updates(map[string]any{
			"total": 0, "expiry_time": 0,
		}).Error; err != nil {
			return err
		}
		var inbounds []model.Inbound
		if err := tx.Select("id", "settings").Find(&inbounds).Error; err != nil {
			return err
		}
		for _, ib := range inbounds {
			cleared, changed := clearSettingsClientLimits(ib.Settings)
			if !changed {
				continue
			}
			if err := tx.Model(&model.Inbound{}).Where("id = ?", ib.Id).Update("settings", cleared).Error; err != nil {
				return err
			}
		}
		return tx.Create(&model.HistoryOfSeeders{SeederName: clientLimitsToConnectionsSeeder}).Error
	})
}

func clearSettingsClientLimits(raw string) (string, bool) {
	var settings map[string]any
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return raw, false
	}
	clients, ok := settings["clients"].([]any)
	if !ok {
		return raw, false
	}
	changed := false
	for _, item := range clients {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for _, key := range []string{"totalGB", "limitIp", "limitHwid", "expiryTime"} {
			if v, present := entry[key]; present && v != float64(0) {
				entry[key] = 0
				changed = true
			}
		}
	}
	if !changed {
		return raw, false
	}
	out, err := json.Marshal(settings)
	if err != nil {
		return raw, false
	}
	return string(out), true
}
