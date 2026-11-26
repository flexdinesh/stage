package remote

func (m *Machine) AgentGateways() []string { return m.hub.Gateways(m.id) }

// Retired discards the proxy. The server's active connection record controls
// whether this machine can serve the connection again.
func (m *Machine) Retired(connection int64) {
	m.mu.Lock()
	delete(m.adapters, connection)
	m.mu.Unlock()
	if m.hosts != nil {
		m.hosts.changed()
	}
}
