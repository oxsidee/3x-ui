package sub

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

type subConnection struct {
	InboundId  int
	Email      string
	Remark     string
	TotalGB    int64
	LimitIP    int
	LimitHwid  int
	ExpiryTime int64
	Up         int64
	Down       int64
}

func (c subConnection) link() model.ClientInbound {
	return model.ClientInbound{InboundId: c.InboundId, TotalGB: c.TotalGB, LimitIP: c.LimitIP, LimitHwid: c.LimitHwid, ExpiryTime: c.ExpiryTime, Up: c.Up, Down: c.Down}
}

func loadSubConnections(subId string) []subConnection {
	var rows []subConnection
	if err := database.GetDB().Table("client_inbounds").
		Select("client_inbounds.inbound_id, clients.email, inbounds.remark, client_inbounds.total_gb, client_inbounds.limit_ip, client_inbounds.limit_hwid, client_inbounds.expiry_time, client_inbounds.up, client_inbounds.down").
		Joins("JOIN clients ON clients.id = client_inbounds.client_id").
		Joins("JOIN inbounds ON inbounds.id = client_inbounds.inbound_id").
		Where("clients.sub_id = ? AND inbounds.enable = ?", subId, true).
		Order("inbounds.sub_sort_index asc, inbounds.id asc").
		Scan(&rows).Error; err != nil {
		logger.Warning("subscription: load connection limits:", err)
		return nil
	}
	return rows
}

func (s *SubService) connectionsFor(subId string) []subConnection {
	if s.subConnections == nil {
		s.subConnections = map[string][]subConnection{}
	}
	if rows, ok := s.subConnections[subId]; ok {
		return rows
	}
	rows := loadSubConnections(subId)
	s.subConnections[subId] = rows
	return rows
}

func (s *SubService) connectionHidden(inbound *model.Inbound, email, subId string) bool {
	for _, c := range s.connectionsFor(subId) {
		if c.InboundId != inbound.Id || !strings.EqualFold(c.Email, email) {
			continue
		}
		link := c.link()
		if link.Expired(time.Now().UnixMilli()) {
			return true
		}
		if link.LimitHwid > 0 {
			allowed, err := service.HwidAmongFirstDevices(subId, s.device, link.LimitHwid)
			if err != nil {
				logger.Warning("subscription: hwid check:", err)
			}
			return !allowed
		}
		return false
	}
	return false
}

const connectionLimitsAnnounceMax = 200

func formatGiB(bytes int64) string {
	gib := math.Round(float64(bytes)/float64(1<<30)*100) / 100
	return strconv.FormatFloat(gib, 'f', -1, 64)
}

func (s *SubService) ConnectionLimitsBanner(subId string) string {
	now := time.Now()
	var lines []string
	for _, c := range s.connectionsFor(subId) {
		link := c.link()
		var parts []string
		if c.TotalGB > 0 {
			used := c.Up + c.Down
			left := max(c.TotalGB-used, 0)
			parts = append(parts, fmt.Sprintf("использовано %s/%s ГБ, осталось %s ГБ", formatGiB(used), formatGiB(c.TotalGB), formatGiB(left)))
		}
		switch {
		case c.ExpiryTime < 0:
			parts = append(parts, fmt.Sprintf("%d дн. с первого подключения", -c.ExpiryTime/86400000))
		case link.Expired(now.UnixMilli()):
			parts = append(parts, "срок истёк")
		case c.ExpiryTime > 0:
			parts = append(parts, "до "+time.UnixMilli(c.ExpiryTime).In(time.Local).Format("02.01.2006"))
		}
		if len(parts) == 0 {
			continue
		}
		lines = append(lines, c.Remark+": "+strings.Join(parts, ", "))
	}
	if len(lines) == 0 {
		return ""
	}
	return clampRunes("Лимиты\n"+strings.Join(lines, "\n"), connectionLimitsAnnounceMax)
}

func clampRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit-1]) + "…"
}

type SubPageConnection struct {
	Remark     string `json:"remark"`
	TotalByte  int64  `json:"totalByte"`
	UsedByte   int64  `json:"usedByte"`
	ExpiryTime int64  `json:"expiryTime"`
	Expired    bool   `json:"expired"`
	LimitIP    int    `json:"limitIp"`
	LimitHwid  int    `json:"limitHwid"`
}

func (s *SubService) ConnectionLimitsView(subId string) []SubPageConnection {
	now := time.Now().UnixMilli()
	out := []SubPageConnection{}
	for _, c := range s.connectionsFor(subId) {
		link := c.link()
		if !link.HasLimits() {
			continue
		}
		out = append(out, SubPageConnection{
			Remark:     c.Remark,
			TotalByte:  c.TotalGB,
			UsedByte:   c.Up + c.Down,
			ExpiryTime: c.ExpiryTime,
			Expired:    link.Expired(now),
			LimitIP:    c.LimitIP,
			LimitHwid:  c.LimitHwid,
		})
	}
	return out
}
