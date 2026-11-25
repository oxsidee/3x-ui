package service

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/runtime"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

type ConnectionLimitResolver struct{}

type nodeConnection struct {
	ClientId   int
	InboundId  int
	Email      string
	Tag        string
	Protocol   model.Protocol
	TotalGB    int64
	LimitIP    int
	LimitHwid  int
	ExpiryTime int64
	Up         int64
	Down       int64
}

func (c nodeConnection) link() model.ClientInbound {
	return model.ClientInbound{ClientId: c.ClientId, InboundId: c.InboundId, TotalGB: c.TotalGB, LimitIP: c.LimitIP, LimitHwid: c.LimitHwid, ExpiryTime: c.ExpiryTime, Up: c.Up, Down: c.Down}
}

func loadNodeConnections(nodeID int) ([]nodeConnection, error) {
	var rows []nodeConnection
	err := database.GetDB().Table("client_inbounds").
		Select("client_inbounds.client_id, client_inbounds.inbound_id, clients.email, inbounds.tag, inbounds.protocol, client_inbounds.total_gb, client_inbounds.limit_ip, client_inbounds.limit_hwid, client_inbounds.expiry_time, client_inbounds.up, client_inbounds.down").
		Joins("JOIN clients ON clients.id = client_inbounds.client_id").
		Joins("JOIN inbounds ON inbounds.id = client_inbounds.inbound_id").
		Where("inbounds.node_id = ? AND inbounds.protocol IN ?", nodeID, connectionLimitProtocolList()).
		Scan(&rows).Error
	return rows, err
}

func connectionLimitProtocolList() []string {
	out := make([]string, 0, len(connectionLimitProtocols))
	for p := range connectionLimitProtocols {
		out = append(out, string(p))
	}
	slices.Sort(out)
	return out
}

func supportsConnectionLimits(protocol model.Protocol) bool {
	_, ok := connectionLimitProtocols[protocol]
	return ok
}

func nodeTagSet(nodeID int) map[string]struct{} {
	var tags []string
	if err := database.GetDB().Model(&model.Inbound{}).Where("node_id = ? AND protocol IN ?", nodeID, connectionLimitProtocolList()).Pluck("tag", &tags).Error; err != nil {
		logger.Warning("connection limits: load node tags:", err)
	}
	set := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		set[runtime.NodeSideTag(nodeID, tag)] = struct{}{}
	}
	return set
}

func splitConnectionEmail(email string, tags map[string]struct{}) (string, string, bool) {
	idx := strings.LastIndex(email, "~")
	if idx <= 0 {
		return email, "", false
	}
	tag := email[idx+1:]
	if _, ok := tags[tag]; !ok {
		return email, "", false
	}
	return email[:idx], tag, true
}

func connectionsByEmail(conns []nodeConnection, inboundID int) map[string]nodeConnection {
	out := make(map[string]nodeConnection)
	for _, c := range conns {
		if c.InboundId == inboundID {
			out[c.Email] = c
		}
	}
	return out
}

func (r ConnectionLimitResolver) ProjectInboundSettings(nodeID int, ib *model.Inbound, nodeTag string) string {
	if ib == nil {
		return ""
	}
	if !supportsConnectionLimits(ib.Protocol) || strings.TrimSpace(ib.Settings) == "" {
		return ib.Settings
	}
	var settings map[string]any
	if err := json.Unmarshal([]byte(ib.Settings), &settings); err != nil {
		return ib.Settings
	}
	clients, ok := settings["clients"].([]any)
	if !ok || len(clients) == 0 {
		return ib.Settings
	}
	conns, err := loadNodeConnections(nodeID)
	if err != nil {
		logger.Warning("connection limits: load connections:", err)
	}
	limits := connectionsByEmail(conns, ib.Id)
	for _, raw := range clients {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		email, _ := entry["email"].(string)
		if email == "" {
			continue
		}
		entry["email"] = runtime.ConnectionClientEmail(email, nodeTag)
		if conn, ok := limits[email]; ok {
			applyConnectionLimitsToEntry(entry, conn.link())
		}
	}
	out, err := json.Marshal(settings)
	if err != nil {
		return ib.Settings
	}
	return string(out)
}

func applyConnectionLimitsToEntry(entry map[string]any, link model.ClientInbound) {
	if link.TotalGB > 0 {
		entry["totalGB"] = link.TotalGB
	}
	if link.LimitIP > 0 {
		entry["limitIp"] = link.LimitIP
	}
	if link.ExpiryTime != 0 {
		entry["expiryTime"] = link.ExpiryTime
	}
	if link.TrafficExhausted() || link.Expired(time.Now().UnixMilli()) {
		entry["enable"] = false
	}
}

func (r ConnectionLimitResolver) ConnectionEmail(nodeID int, ib *model.Inbound, nodeTag, email string) string {
	if ib == nil || !supportsConnectionLimits(ib.Protocol) {
		return email
	}
	return runtime.ConnectionClientEmail(email, nodeTag)
}

func (r ConnectionLimitResolver) ProjectClient(nodeID int, ib *model.Inbound, nodeTag string, client model.Client) model.Client {
	if ib == nil || client.Email == "" || !supportsConnectionLimits(ib.Protocol) {
		return client
	}
	conns, err := loadNodeConnections(nodeID)
	if err != nil {
		logger.Warning("connection limits: load connections:", err)
	}
	if conn, ok := connectionsByEmail(conns, ib.Id)[client.Email]; ok {
		link := conn.link()
		if link.TotalGB > 0 {
			client.TotalGB = link.TotalGB
		}
		if link.LimitIP > 0 {
			client.LimitIP = link.LimitIP
		}
		if link.ExpiryTime != 0 {
			client.ExpiryTime = link.ExpiryTime
		}
		if link.TrafficExhausted() || link.Expired(time.Now().UnixMilli()) {
			client.Enable = false
		}
	}
	client.Email = runtime.ConnectionClientEmail(client.Email, nodeTag)
	return client
}

func (r ConnectionLimitResolver) NodeEmails(nodeID int, emails []string) []string {
	conns, err := loadNodeConnections(nodeID)
	if err != nil {
		logger.Warning("connection limits: load connections:", err)
		return emails
	}
	byEmail := make(map[string][]string)
	for _, c := range conns {
		byEmail[c.Email] = append(byEmail[c.Email], runtime.ConnectionClientEmail(c.Email, runtime.NodeSideTag(nodeID, c.Tag)))
	}
	out := make([]string, 0, len(emails))
	for _, email := range emails {
		if wires, ok := byEmail[email]; ok {
			out = append(out, wires...)
			continue
		}
		out = append(out, email)
	}
	return out
}

type connectionUsage struct {
	up, down int64
	expiry   int64
}

func (r ConnectionLimitResolver) TranslateSnapshot(nodeID int, snap *runtime.TrafficSnapshot) {
	if snap == nil {
		return
	}
	tags := nodeTagSet(nodeID)
	if len(tags) == 0 {
		return
	}
	baseMax := make(map[string]connectionUsage)
	wireMax := make(map[string]map[string]connectionUsage)
	baseRow := make(map[string]xray.ClientTraffic)
	for _, ib := range snap.Inbounds {
		if ib == nil {
			continue
		}
		for _, cs := range ib.ClientStats {
			base, tag, wired := splitConnectionEmail(cs.Email, tags)
			if !wired {
				cur := baseMax[cs.Email]
				cur.up = max(cur.up, cs.Up)
				cur.down = max(cur.down, cs.Down)
				baseMax[cs.Email] = cur
				if _, seen := baseRow[cs.Email]; !seen {
					baseRow[cs.Email] = cs
				}
				continue
			}
			if wireMax[base] == nil {
				wireMax[base] = make(map[string]connectionUsage)
			}
			cur := wireMax[base][tag]
			cur.up = max(cur.up, cs.Up)
			cur.down = max(cur.down, cs.Down)
			cur.expiry = max(cur.expiry, cs.ExpiryTime)
			wireMax[base][tag] = cur
		}
	}

	bases := make([]string, 0, len(wireMax))
	for base := range wireMax {
		bases = append(bases, base)
	}
	records := loadClientRecords(bases)
	masterRows := loadMasterTraffic(bases)

	combined := make(map[string]connectionUsage, len(wireMax))
	for base, perTag := range wireMax {
		sum := baseMax[base]
		for _, u := range perTag {
			sum.up += u.up
			sum.down += u.down
		}
		combined[base] = sum
	}

	for _, ib := range snap.Inbounds {
		if ib == nil {
			continue
		}
		ib.Settings = translateSnapshotSettings(ib.Settings, tags, records)
		for i := range ib.ClientStats {
			cs := &ib.ClientStats[i]
			base, _, wired := splitConnectionEmail(cs.Email, tags)
			total, hasWires := combined[base]
			if !hasWires {
				continue
			}
			if wired {
				lastOnline := cs.LastOnline
				if row, ok := baseRow[base]; ok {
					copyClientLifecycle(cs, row)
				} else if row, ok := masterRows[base]; ok {
					copyClientLifecycle(cs, row)
				}
				cs.Email = base
				cs.LastOnline = max(lastOnline, cs.LastOnline)
			}
			cs.Up = total.up
			cs.Down = total.down
		}
	}

	snap.OnlineEmails = translateEmailList(snap.OnlineEmails, tags)
	for guid, emails := range snap.OnlineTree {
		snap.OnlineTree[guid] = translateEmailList(emails, tags)
	}
	if len(snap.LastOnlineMap) > 0 {
		translated := make(map[string]int64, len(snap.LastOnlineMap))
		for email, ts := range snap.LastOnlineMap {
			base, _, _ := splitConnectionEmail(email, tags)
			translated[base] = max(translated[base], ts)
		}
		snap.LastOnlineMap = translated
	}

	recordConnectionUsage(nodeID, wireMax)
}

func copyClientLifecycle(dst *xray.ClientTraffic, src xray.ClientTraffic) {
	dst.Enable = src.Enable
	dst.Total = src.Total
	dst.ExpiryTime = src.ExpiryTime
	dst.Reset = src.Reset
	dst.ResetDay = src.ResetDay
	dst.ResetWeekday = src.ResetWeekday
	dst.ResetMax = src.ResetMax
	dst.ResetCount = src.ResetCount
	dst.LastOnline = src.LastOnline
}

func translateEmailList(emails []string, tags map[string]struct{}) []string {
	if len(emails) == 0 {
		return emails
	}
	out := make([]string, 0, len(emails))
	seen := make(map[string]struct{}, len(emails))
	for _, email := range emails {
		base, _, _ := splitConnectionEmail(email, tags)
		if _, dup := seen[base]; dup {
			continue
		}
		seen[base] = struct{}{}
		out = append(out, base)
	}
	return out
}

func translateSnapshotSettings(raw string, tags map[string]struct{}, records map[string]model.ClientRecord) string {
	if strings.TrimSpace(raw) == "" {
		return raw
	}
	var settings map[string]any
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return raw
	}
	clients, ok := settings["clients"].([]any)
	if !ok {
		return raw
	}
	changed := false
	for _, item := range clients {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		email, _ := entry["email"].(string)
		base, _, wired := splitConnectionEmail(email, tags)
		if !wired {
			continue
		}
		changed = true
		entry["email"] = base
		if rec, ok := records[base]; ok {
			entry["totalGB"] = rec.TotalGB
			entry["limitIp"] = rec.LimitIP
			entry["expiryTime"] = rec.ExpiryTime
			entry["enable"] = rec.Enable
		}
	}
	if !changed {
		return raw
	}
	out, err := json.Marshal(settings)
	if err != nil {
		return raw
	}
	return string(out)
}

func loadClientRecords(emails []string) map[string]model.ClientRecord {
	out := make(map[string]model.ClientRecord, len(emails))
	for _, batch := range chunkStrings(emails, sqlInChunk) {
		var rows []model.ClientRecord
		if err := database.GetDB().Where("email IN ?", batch).Find(&rows).Error; err != nil {
			logger.Warning("connection limits: load clients:", err)
			continue
		}
		for _, row := range rows {
			out[row.Email] = row
		}
	}
	return out
}

func loadMasterTraffic(emails []string) map[string]xray.ClientTraffic {
	out := make(map[string]xray.ClientTraffic, len(emails))
	for _, batch := range chunkStrings(emails, sqlInChunk) {
		var rows []xray.ClientTraffic
		if err := database.GetDB().Where("email IN ?", batch).Find(&rows).Error; err != nil {
			logger.Warning("connection limits: load traffic:", err)
			continue
		}
		for _, row := range rows {
			out[row.Email] = row
		}
	}
	return out
}

func recordConnectionUsage(nodeID int, wireMax map[string]map[string]connectionUsage) {
	if len(wireMax) == 0 {
		return
	}
	conns, err := loadNodeConnections(nodeID)
	if err != nil {
		logger.Warning("connection limits: load connections:", err)
		return
	}
	db := database.GetDB()
	flipped := false
	for _, c := range conns {
		u, ok := wireMax[c.Email][runtime.NodeSideTag(nodeID, c.Tag)]
		if !ok {
			continue
		}
		updates := map[string]any{}
		if u.up != c.Up || u.down != c.Down {
			updates["up"], updates["down"] = u.up, u.down
		}
		if c.ExpiryTime < 0 && u.expiry > 0 {
			updates["expiry_time"] = u.expiry
		}
		if len(updates) == 0 {
			continue
		}
		before := c.link().TrafficExhausted()
		c.Up, c.Down = u.up, u.down
		if err := db.Model(&model.ClientInbound{}).
			Where("client_id = ? AND inbound_id = ?", c.ClientId, c.InboundId).
			Updates(updates).Error; err != nil {
			logger.Warning("connection limits: save usage:", err)
			continue
		}
		if c.link().TrafficExhausted() != before {
			flipped = true
		}
	}
	if flipped {
		if err := (&NodeService{}).MarkNodeDirty(nodeID); err != nil {
			logger.Warning("connection limits: mark node dirty:", err)
		}
	}
}

func mergeIpLists(dst, src string) string {
	var a, b []model.ClientIpEntry
	_ = json.Unmarshal([]byte(dst), &a)
	_ = json.Unmarshal([]byte(src), &b)
	byIP := make(map[string]int, len(a)+len(b))
	for i, e := range a {
		byIP[e.IP] = i
	}
	for _, e := range b {
		if i, ok := byIP[e.IP]; ok {
			a[i].Timestamp = max(a[i].Timestamp, e.Timestamp)
			continue
		}
		byIP[e.IP] = len(a)
		a = append(a, e)
	}
	out, err := json.Marshal(a)
	if err != nil {
		return dst
	}
	return string(out)
}

func (r ConnectionLimitResolver) TranslateClientIps(nodeID int, ips []model.InboundClientIps) []model.InboundClientIps {
	tags := nodeTagSet(nodeID)
	if len(tags) == 0 {
		return ips
	}
	index := make(map[string]int, len(ips))
	out := make([]model.InboundClientIps, 0, len(ips))
	for _, row := range ips {
		base, _, _ := splitConnectionEmail(row.ClientEmail, tags)
		if i, ok := index[base]; ok {
			out[i].Ips = mergeIpLists(out[i].Ips, row.Ips)
			continue
		}
		row.ClientEmail = base
		index[base] = len(out)
		out = append(out, row)
	}
	return out
}

func (r ConnectionLimitResolver) TranslateClientIpsByGuid(nodeID int, byGuid map[string]map[string][]model.ClientIpEntry) {
	tags := nodeTagSet(nodeID)
	if len(tags) == 0 {
		return
	}
	for guid, byEmail := range byGuid {
		merged := make(map[string][]model.ClientIpEntry, len(byEmail))
		for email, entries := range byEmail {
			base, _, _ := splitConnectionEmail(email, tags)
			merged[base] = mergeIpEntries(merged[base], entries)
		}
		byGuid[guid] = merged
	}
}

func mergeIpEntries(dst, src []model.ClientIpEntry) []model.ClientIpEntry {
	for _, e := range src {
		i := slices.IndexFunc(dst, func(x model.ClientIpEntry) bool { return x.IP == e.IP })
		if i >= 0 {
			dst[i].Timestamp = max(dst[i].Timestamp, e.Timestamp)
			continue
		}
		dst = append(dst, e)
	}
	return dst
}

func (r ConnectionLimitResolver) ProjectClientIps(nodeID int, ips []model.InboundClientIps) []model.InboundClientIps {
	conns, err := loadNodeConnections(nodeID)
	if err != nil {
		logger.Warning("connection limits: load connections:", err)
		return ips
	}
	inherit := make(map[string][]string)
	for _, c := range conns {
		if c.LimitIP > 0 {
			continue
		}
		inherit[c.Email] = append(inherit[c.Email], runtime.ConnectionClientEmail(c.Email, runtime.NodeSideTag(nodeID, c.Tag)))
	}
	out := make([]model.InboundClientIps, 0, len(ips))
	for _, row := range ips {
		wires, ok := inherit[row.ClientEmail]
		if !ok {
			out = append(out, row)
			continue
		}
		for _, wire := range wires {
			out = append(out, model.InboundClientIps{ClientEmail: wire, Ips: row.Ips})
		}
	}
	return out
}

type ConnectionLimitInput struct {
	InboundId  int   `json:"inboundId"`
	TotalGB    int64 `json:"totalGB"`
	LimitIP    int   `json:"limitIp"`
	LimitHwid  int   `json:"limitHwid"`
	ExpiryTime int64 `json:"expiryTime"`
}

func (in ConnectionLimitInput) hasLimits() bool {
	return in.TotalGB > 0 || in.LimitIP > 0 || in.LimitHwid > 0 || in.ExpiryTime != 0
}

var connectionLimitProtocols = map[model.Protocol]struct{}{
	model.VLESS:       {},
	model.VMESS:       {},
	model.Trojan:      {},
	model.Shadowsocks: {},
}

func (s *ClientService) ConnectionLimits(clientId int) ([]model.ClientInbound, error) {
	var links []model.ClientInbound
	err := database.GetDB().Where("client_id = ?", clientId).Order("inbound_id").Find(&links).Error
	return links, err
}

func (s *ClientService) SetConnectionLimits(clientId int, inputs []ConnectionLimitInput) ([]model.ClientInbound, error) {
	db := database.GetDB()
	links, err := s.ConnectionLimits(clientId)
	if err != nil {
		return nil, err
	}
	byInbound := make(map[int]model.ClientInbound, len(links))
	for _, l := range links {
		byInbound[l.InboundId] = l
	}
	dirtyNodes := make(map[int]struct{})
	for _, in := range inputs {
		link, ok := byInbound[in.InboundId]
		if !ok {
			return nil, fmt.Errorf("client is not attached to inbound %d", in.InboundId)
		}
		if in.TotalGB < 0 || in.LimitIP < 0 || in.LimitHwid < 0 {
			return nil, fmt.Errorf("connection limits must not be negative")
		}
		if in.TotalGB == link.TotalGB && in.LimitIP == link.LimitIP && in.LimitHwid == link.LimitHwid && in.ExpiryTime == link.ExpiryTime {
			continue
		}
		var inbound model.Inbound
		if err := db.Select("id", "node_id", "protocol").First(&inbound, in.InboundId).Error; err != nil {
			return nil, err
		}
		if in.hasLimits() && inbound.NodeID == nil {
			return nil, fmt.Errorf("connection limits are only supported on node inbounds (inbound %d is local)", in.InboundId)
		}
		if !supportsConnectionLimits(inbound.Protocol) && in.hasLimits() {
			return nil, fmt.Errorf("connection limits are not supported for protocol %s", inbound.Protocol)
		}
		if err := db.Model(&model.ClientInbound{}).
			Where("client_id = ? AND inbound_id = ?", clientId, in.InboundId).
			Updates(map[string]any{"total_gb": in.TotalGB, "limit_ip": in.LimitIP, "limit_hwid": in.LimitHwid, "expiry_time": in.ExpiryTime}).Error; err != nil {
			return nil, err
		}
		if inbound.NodeID != nil {
			dirtyNodes[*inbound.NodeID] = struct{}{}
		}
	}
	for nodeID := range dirtyNodes {
		if err := (&NodeService{}).MarkNodeDirty(nodeID); err != nil {
			return nil, err
		}
	}
	return s.ConnectionLimits(clientId)
}
