package sub

import (
	"fmt"
	"math/rand/v2"
	"slices"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func hidingSubBalancers() []model.SubBalancer {
	var hiding []model.SubBalancer
	for _, b := range getEnabledSubBalancers() {
		if b.HideMembers {
			hiding = append(hiding, b)
		}
	}
	return hiding
}

func (s *SubService) inboundNodeOnline(inbound *model.Inbound) bool {
	if inbound.NodeID == nil {
		return true
	}
	n, ok := s.nodesByID[*inbound.NodeID]
	return ok && n.Enable && n.Status == "online"
}

func (s *SubService) rawBalancerSkips(inbounds []*model.Inbound, subId string) map[int]struct{} {
	balancers := hidingSubBalancers()
	if len(balancers) == 0 {
		return nil
	}
	kept := make(map[int]struct{})
	members := make(map[int]struct{})
	for _, b := range balancers {
		var candidates, online []*model.Inbound
		for _, inbound := range inbounds {
			if inbound.ExcludeFromSub || !slices.Contains(b.InboundIds, inbound.Id) || len(s.matchingClients(inbound, subId)) == 0 {
				continue
			}
			members[inbound.Id] = struct{}{}
			candidates = append(candidates, inbound)
			if s.inboundNodeOnline(inbound) {
				online = append(online, inbound)
			}
		}
		if len(online) > 0 {
			candidates = online
		}
		if len(candidates) > 0 {
			kept[candidates[rand.IntN(len(candidates))].Id] = struct{}{}
		}
	}
	skips := make(map[int]struct{})
	for id := range members {
		if _, keep := kept[id]; !keep {
			skips[id] = struct{}{}
		}
	}
	return skips
}

func hiddenBalancerMembers(balancers []model.SubBalancer, rendered map[int]bool) map[int]struct{} {
	hidden := make(map[int]struct{})
	for _, b := range balancers {
		if !b.HideMembers || !rendered[b.Id] {
			continue
		}
		for _, id := range b.InboundIds {
			hidden[id] = struct{}{}
		}
	}
	return hidden
}

type ownedProxies struct {
	inboundId int
	proxies   []map[string]any
}

func clashBalancerGroupType(strategy string) map[string]any {
	switch strategy {
	case "random", "roundRobin":
		return map[string]any{"type": "load-balance", "strategy": "round-robin"}
	}
	return map[string]any{"type": "url-test", "tolerance": 50}
}

type clashBalancerGroups struct {
	groups    []map[string]any
	groupOf   map[string]string
	hiddenSet map[string]struct{}
}

func buildClashBalancerGroups(proxies []map[string]any, owners []int) clashBalancerGroups {
	result := clashBalancerGroups{groupOf: map[string]string{}, hiddenSet: map[string]struct{}{}}
	balancers := getEnabledSubBalancers()
	if len(balancers) == 0 {
		return result
	}
	taken := make(map[string]struct{}, len(proxies))
	for _, p := range proxies {
		if name, ok := p["name"].(string); ok {
			taken[name] = struct{}{}
		}
	}
	type pending struct {
		balancer model.SubBalancer
		members  []map[string]any
	}
	var plans []pending
	groupNames := make(map[string]struct{})
	for _, b := range balancers {
		var members []map[string]any
		for i, p := range proxies {
			if owners[i] != 0 && !isDummyProxy(p) && slices.Contains(b.InboundIds, owners[i]) {
				members = append(members, p)
			}
		}
		if len(members) == 0 {
			continue
		}
		name := b.Remark
		for n := 2; ; n++ {
			if _, dup := groupNames[name]; !dup && name != "PROXY" && name != "DIRECT" {
				break
			}
			name = fmt.Sprintf("%s-%d", b.Remark, n)
		}
		groupNames[name] = struct{}{}
		b.Remark = name
		plans = append(plans, pending{b, members})
	}
	for _, p := range proxies {
		name, _ := p["name"].(string)
		if _, clash := groupNames[name]; !clash {
			continue
		}
		renamed := name
		for n := 2; ; n++ {
			renamed = fmt.Sprintf("%s-%d", name, n)
			_, usedProxy := taken[renamed]
			_, usedGroup := groupNames[renamed]
			if !usedProxy && !usedGroup {
				break
			}
		}
		taken[renamed] = struct{}{}
		p["name"] = renamed
	}
	for _, plan := range plans {
		names := make([]string, 0, len(plan.members))
		for _, m := range plan.members {
			name := m["name"].(string)
			names = append(names, name)
			if _, seen := result.groupOf[name]; !seen {
				result.groupOf[name] = plan.balancer.Remark
			}
			if plan.balancer.HideMembers {
				result.hiddenSet[name] = struct{}{}
			}
		}
		group := map[string]any{
			"name":     plan.balancer.Remark,
			"proxies":  names,
			"url":      subBalancerProbeURL,
			"interval": 300,
		}
		for k, v := range clashBalancerGroupType(plan.balancer.Strategy) {
			group[k] = v
		}
		result.groups = append(result.groups, group)
	}
	return result
}

func (g clashBalancerGroups) selectorNames(proxies []map[string]any) []string {
	names := make([]string, 0, len(proxies)+len(g.groups)+1)
	listed := make(map[string]struct{})
	for _, proxy := range proxies {
		if isDummyProxy(proxy) && len(proxies) > 1 {
			continue
		}
		name, ok := proxy["name"].(string)
		if !ok || name == "" {
			continue
		}
		if group, member := g.groupOf[name]; member {
			if _, done := listed[group]; !done {
				listed[group] = struct{}{}
				names = append(names, group)
			}
		}
		if _, hidden := g.hiddenSet[name]; hidden {
			continue
		}
		names = append(names, name)
	}
	return append(names, "DIRECT")
}
