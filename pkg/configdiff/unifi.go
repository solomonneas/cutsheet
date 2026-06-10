package configdiff

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
)

type unifiJSONParser struct{}

var _ Parser = unifiJSONParser{}

// knownUnifiCollections are the top-level keyed sections handled with dedicated semantics.
// Any other key is flattened generically so nothing is silently dropped.
var knownUnifiCollections = map[string]bool{
	"networkconf":    true,
	"port_overrides": true,
	"portconf":       true,
	"firewallrule":   true,
	"firewallgroup":  true,
	"routing":        true,
	"wlanconf":       true,
}

// Parse reads a UniFi Network controller JSON export and flattens it into pseudo-line
// configBlocks so the existing diff/analyzer/report pipeline runs unchanged. Block IDs key on
// stable content identifiers (name, _id, port_idx, prefix), never array index, because JSON
// ordering is not guaranteed across exports. Each container also emits CLI-equivalent readable
// lines so the existing and Unit 1 detectors fire without a JSON-specific risk engine.
func (unifiJSONParser) Parse(text string, requestedVendor string) parsedConfig {
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &root); err != nil {
		parsed := parseGeneric(text, requestedVendor)
		parsed.Detection.Parser = "unifi-json"
		parsed.Detection.DetectedVendor = "ubiquiti"
		parsed.Detection.Confidence = 0.30
		parsed.Detection.Signals = appendSignal(parsed.Detection.Signals, "requested unifi-json but input was not valid JSON; treated generically")
		return parsed
	}

	idToVlan := unifiNetworkVLANMap(root)
	blocks := []configBlock{}

	for _, e := range unifiArray(root, "networkconf") {
		name := firstNonEmpty(unifiScalar(e["name"]), unifiScalar(e["_id"]), unifiScalar(e["vlan"]))
		lines := unifiLeafLines("networkconf["+strings.ToLower(name)+"]", e)
		if v := unifiScalar(e["vlan"]); v != "" && v != "null" {
			lines = append(lines, "vlan "+v)
		}
		blocks = append(blocks, configBlock{ID: "vlan:" + strings.ToLower(name), Kind: "vlan", Header: "networkconf " + name, Lines: lines})
	}

	for _, e := range unifiArray(root, "portconf") {
		name := firstNonEmpty(unifiScalar(e["name"]), unifiScalar(e["_id"]))
		lines := unifiLeafLines("portconf["+strings.ToLower(name)+"]", e)
		blocks = append(blocks, configBlock{ID: "interface:portconf-" + strings.ToLower(name), Kind: "interface", Header: "portconf " + name, Lines: lines})
	}

	for _, e := range unifiArray(root, "port_overrides") {
		key := firstNonEmpty(unifiScalar(e["port_idx"]), unifiScalar(e["_id"]))
		lines := unifiLeafLines("port_overrides[port_idx="+key+"]", e)
		if om := strings.ToLower(unifiScalar(e["op_mode"])); om != "" && om != "null" {
			mode := "trunk"
			if om == "access" {
				mode = "access"
			}
			lines = append(lines, "switchport mode "+mode)
		}
		if nid := unifiScalar(e["native_networkconf_id"]); nid != "" && nid != "null" {
			if v := idToVlan[nid]; v != "" {
				lines = append(lines, "switchport trunk native vlan "+v)
			} else {
				lines = append(lines, "unresolved native_networkconf_id "+nid)
			}
		}
		if tagged := unifiVLANList(e["tagged_networkconf_ids"], idToVlan); tagged != "" {
			lines = append(lines, "switchport trunk allowed vlan "+tagged)
		}
		switch strings.ToLower(unifiScalar(e["forward"])) {
		case "disabled":
			lines = append(lines, "shutdown")
		case "":
		default:
			lines = append(lines, "no shutdown")
		}
		blocks = append(blocks, configBlock{ID: "interface:port-" + strings.ToLower(key), Kind: "interface", Header: "port " + key, Lines: lines})
	}

	for _, e := range unifiArray(root, "firewallrule") {
		name := firstNonEmpty(unifiScalar(e["name"]), unifiScalar(e["ruleset"])+"-"+unifiScalar(e["rule_index"]), unifiScalar(e["_id"]))
		lines := unifiLeafLines("firewallrule["+strings.ToLower(name)+"]", e)
		action := strings.ToLower(unifiScalar(e["action"]))
		if action == "accept" || action == "allow" {
			lines = append(lines, "permit ip "+unifiAddrToken(e["src_address"], e["src_firewallgroup_ids"])+" "+unifiAddrToken(e["dst_address"], e["dst_firewallgroup_ids"]))
		}
		blocks = append(blocks, configBlock{ID: "firewall:" + strings.ToLower(name), Kind: "firewall", Header: "firewallrule " + name, Lines: lines})
	}

	for _, e := range unifiArray(root, "firewallgroup") {
		name := firstNonEmpty(unifiScalar(e["name"]), unifiScalar(e["_id"]))
		lines := unifiLeafLines("firewallgroup["+strings.ToLower(name)+"]", e)
		blocks = append(blocks, configBlock{ID: "firewall:fwgroup-" + strings.ToLower(name), Kind: "firewall", Header: "firewallgroup " + name, Lines: lines})
	}

	for _, e := range unifiArray(root, "routing") {
		network := firstNonEmpty(unifiScalar(e["static-route_network"]), unifiScalar(e["gateway_network"]))
		name := firstNonEmpty(network, unifiScalar(e["name"]), unifiScalar(e["_id"]))
		lines := unifiLeafLines("routing["+strings.ToLower(name)+"]", e)
		gw := firstNonEmpty(unifiScalar(e["static-route_nexthop"]), unifiScalar(e["gateway"]))
		if network != "" {
			if strings.HasSuffix(network, "/0") {
				lines = append(lines, "ip route 0.0.0.0 0.0.0.0 "+gw)
			} else {
				lines = append(lines, "ip route "+network+" "+gw)
			}
		}
		blocks = append(blocks, configBlock{ID: "route:" + strings.ToLower(name), Kind: "route", Header: "routing " + name, Lines: lines})
	}

	for _, e := range unifiArray(root, "wlanconf") {
		name := firstNonEmpty(unifiScalar(e["name"]), unifiScalar(e["_id"]))
		lines := unifiLeafLines("wlanconf["+strings.ToLower(name)+"]", e)
		blocks = append(blocks, configBlock{ID: "management:wlan-" + strings.ToLower(name), Kind: "management", Header: "wlanconf " + name, Lines: lines})
	}

	keys := make([]string, 0, len(root))
	for k := range root {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if knownUnifiCollections[k] {
			continue
		}
		var value any
		if err := json.Unmarshal(root[k], &value); err != nil {
			continue
		}
		lines := []string{}
		unifiFlatten(strings.ToLower(k), value, &lines)
		sort.Strings(lines)
		if len(lines) == 0 {
			continue
		}
		blocks = append(blocks, configBlock{ID: "line:" + stableID(strings.ToLower(k)), Kind: "generic", Header: k, Lines: uniquePreserve(lines)})
	}

	blocks = mergeRelatedBlocks(blocks)
	detection := detectPlatform(blocks, requestedVendor)
	detection.Parser = "unifi-json"
	detection.DetectedVendor = "ubiquiti"
	detection.DeviceType = unifiDeviceType(root)
	detection.Confidence = 0.80
	if !strings.EqualFold(requestedVendor, "auto") {
		detection.Confidence = 0.90
	}
	detection.Signals = appendSignal(detection.Signals, "unifi controller json export")
	return parsedConfig{Detection: detection, Blocks: blocks}
}

func looksUnifiJSON(text string) bool {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return false
	}
	lower := strings.ToLower(trimmed)
	for _, token := range []string{"networkconf", "port_overrides", "portconf", "firewallrule", "wlanconf", "native_networkconf_id", "site_id", "\"_id\""} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func unifiDeviceType(root map[string]json.RawMessage) string {
	hasGateway := len(unifiArray(root, "routing")) > 0 || len(unifiArray(root, "firewallrule")) > 0
	hasSwitch := len(unifiArray(root, "port_overrides")) > 0 || len(unifiArray(root, "portconf")) > 0
	switch {
	case hasGateway:
		return "gateway"
	case hasSwitch:
		return "switch"
	case len(unifiArray(root, "wlanconf")) > 0:
		return "wireless"
	default:
		return "network-device"
	}
}

func unifiNetworkVLANMap(root map[string]json.RawMessage) map[string]string {
	idToVlan := map[string]string{}
	for _, e := range unifiArray(root, "networkconf") {
		id := unifiScalar(e["_id"])
		if id == "" {
			id = unifiScalar(e["name"])
		}
		if v := unifiScalar(e["vlan"]); id != "" && v != "" && v != "null" {
			idToVlan[id] = v
		}
	}
	return idToVlan
}

func unifiArray(root map[string]json.RawMessage, key string) []map[string]any {
	raw, ok := root[key]
	if !ok {
		return nil
	}
	var arr []map[string]any
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil
	}
	return arr
}

func unifiVLANList(raw any, idToVlan map[string]string) string {
	items, ok := raw.([]any)
	if !ok {
		return ""
	}
	vlans := []string{}
	for _, item := range items {
		id := unifiScalar(item)
		if v := idToVlan[id]; v != "" {
			vlans = append(vlans, v)
		} else if isNumericToken(id) {
			vlans = append(vlans, id)
		}
	}
	return strings.Join(vlans, ",")
}

func unifiAddrToken(addr any, groups any) string {
	value := strings.ToLower(unifiScalar(addr))
	if value == "" || value == "null" {
		if _, ok := groups.([]any); ok {
			return "any"
		}
		return "any"
	}
	if value == "0.0.0.0/0" || value == "all" || value == "any" {
		return "any"
	}
	return value
}

func unifiLeafLines(prefix string, e map[string]any) []string {
	lines := []string{}
	unifiFlatten(prefix, e, &lines)
	sort.Strings(lines)
	return lines
}

func unifiFlatten(prefix string, v any, out *[]string) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			unifiFlatten(prefix+"."+strings.ToLower(k), t[k], out)
		}
	case []any:
		allScalar := true
		scalars := []string{}
		for _, item := range t {
			switch item.(type) {
			case map[string]any, []any:
				allScalar = false
			default:
				scalars = append(scalars, unifiScalar(item))
			}
		}
		if allScalar {
			*out = append(*out, prefix+" = ["+strings.Join(scalars, ",")+"]")
			return
		}
		for i, item := range t {
			unifiFlatten(prefix+"."+strconv.Itoa(i), item, out)
		}
	default:
		*out = append(*out, prefix+" = "+unifiScalar(v))
	}
}

func unifiScalar(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		if t == math.Trunc(t) && !math.IsInf(t, 0) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return ""
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" && value != "null" && value != "-" {
			return value
		}
	}
	return "unknown"
}
