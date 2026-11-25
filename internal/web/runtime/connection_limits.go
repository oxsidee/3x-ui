package runtime

import (
	"sync/atomic"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

const connectionEmailSeparator = "~"

type ConnectionLimitResolver interface {
	ProjectInboundSettings(nodeID int, ib *model.Inbound, nodeTag string) string
	ProjectClient(nodeID int, ib *model.Inbound, nodeTag string, client model.Client) model.Client
	ConnectionEmail(nodeID int, ib *model.Inbound, nodeTag, email string) string
	NodeEmails(nodeID int, emails []string) []string
	TranslateSnapshot(nodeID int, snap *TrafficSnapshot)
	TranslateClientIps(nodeID int, ips []model.InboundClientIps) []model.InboundClientIps
	TranslateClientIpsByGuid(nodeID int, byGuid map[string]map[string][]model.ClientIpEntry)
	ProjectClientIps(nodeID int, ips []model.InboundClientIps) []model.InboundClientIps
}

type resolverHolder struct {
	resolver ConnectionLimitResolver
}

var connectionLimits atomic.Pointer[resolverHolder]

func SetConnectionLimitResolver(r ConnectionLimitResolver) {
	if r == nil {
		connectionLimits.Store(nil)
		return
	}
	connectionLimits.Store(&resolverHolder{resolver: r})
}

func activeConnectionLimits() ConnectionLimitResolver {
	if h := connectionLimits.Load(); h != nil {
		return h.resolver
	}
	return nil
}

func ConnectionClientEmail(email, nodeTag string) string {
	return email + connectionEmailSeparator + nodeTag
}

func NodeSideTag(nodeID int, tag string) string {
	return stripNodeInboundTagPrefix(nodeID, tag)
}

func projectedSettings(ib *model.Inbound, nodeID int) string {
	if r := activeConnectionLimits(); r != nil && nodeID > 0 {
		return r.ProjectInboundSettings(nodeID, ib, NodeSideTag(nodeID, ib.Tag))
	}
	return ib.Settings
}

func projectedClient(ib *model.Inbound, nodeID int, client model.Client) model.Client {
	if r := activeConnectionLimits(); r != nil && nodeID > 0 {
		return r.ProjectClient(nodeID, ib, NodeSideTag(nodeID, ib.Tag), client)
	}
	return client
}

func projectedEmail(ib *model.Inbound, nodeID int, email string) string {
	if r := activeConnectionLimits(); r != nil && nodeID > 0 && email != "" {
		return r.ConnectionEmail(nodeID, ib, NodeSideTag(nodeID, ib.Tag), email)
	}
	return email
}

func projectedNodeEmails(nodeID int, emails []string) []string {
	if r := activeConnectionLimits(); r != nil && nodeID > 0 {
		return r.NodeEmails(nodeID, emails)
	}
	return emails
}
