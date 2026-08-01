import React, { useCallback, useEffect, useState } from 'react'
import {
  api,
  Button,
  ButtonText,
  Card,
  HStack,
  KeyVal,
  ListHeader,
  Loading,
  Page,
  SectionHeader,
  StatTile,
  StatusDot,
  Text,
  VStack
} from '@spr-networks/plugin-ui'

const PLUGIN_BASE = `/plugins/${api.pluginURI() || 'spr-tamago-demo'}`
const REFRESH_INTERVAL_MS = 5000

const textOrDash = (value) => value || '—'

export default function Plugin() {
  const [status, setStatus] = useState(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)

  const refresh = useCallback(() => {
    return api
      .get(`${PLUGIN_BASE}/status`)
      .then((next) => {
        setStatus(next)
        setError('')
      })
      .catch((err) => {
        setError(`Unable to read kernel status${err?.status ? ` (${err.status})` : ''}.`)
      })
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    refresh()
    const timer = window.setInterval(refresh, REFRESH_INTERVAL_MS)
    return () => window.clearInterval(timer)
  }, [refresh])

  if (loading && !status) {
    return (
      <Page>
        <Loading text="Connecting to the TamaGo kernel..." />
      </Page>
    )
  }

  const network = status?.network || {}
  const networkOnline = network.phase === 'online'

  return (
    <Page>
      <ListHeader
        title="TamaGo Kernel Demo"
        description="Direct-booted Go kernel under krun — no Linux guest"
        mark="tg"
        status={status ? 'Kernel online' : 'Unavailable'}
        statusAction={status ? 'success' : 'error'}
      >
        <Button size="sm" variant="outline" onPress={refresh}>
          <ButtonText>Refresh</ButtonText>
        </Button>
      </ListHeader>

      <Card>
        <SectionHeader
          title="Hello, SPR."
          right={<StatusDot online={!!status} />}
        />
        <Text
          size="lg"
          fontWeight="$semibold"
          color="$primary700"
          sx={{ _dark: { color: '$primary300' } }}
        >
          Hello World from the TamaGo kernel!
        </Text>
        <Text mt="$2" size="sm" color="$muted500">
          This React UI is built with the SPR Plugin UI SDK and embedded into the
          bare-metal kernel image.
        </Text>
      </Card>

      <Card>
        <SectionHeader title="Runtime" />
        <HStack flexWrap="wrap" gap="$2">
          <StatTile label="Runtime" value={`${textOrDash(status?.runtime)}/${textOrDash(status?.arch)}`} mono />
          <StatTile label="TamaGo" value={textOrDash(status?.tamago_version)} mono />
          <StatTile label="Role" value="krun guest kernel" />
          <StatTile label="SPR IPC" value={`${textOrDash(status?.ipc)} · port ${status?.port || '—'}`} mono />
        </HStack>
      </Card>

      <Card tone={error ? 'warning' : 'default'}>
        <SectionHeader
          title="VirtIO network"
          right={<StatusDot online={networkOnline} warn={!!status && !networkOnline} />}
        />
        <VStack space="sm">
          <KeyVal label="State" value={textOrDash(network.phase)} />
          <KeyVal label="Interface" value={`${textOrDash(network.device)} · ${textOrDash(network.mac)}`} mono />
          <KeyVal label="DHCP address" value={textOrDash(network.address)} mono />
          <KeyVal label="Gateway" value={textOrDash(network.gateway)} mono />
          <KeyVal label="DNS" value={network.dns?.length ? network.dns.join(', ') : '—'} mono />
          <KeyVal label="Lease" value={textOrDash(network.lease)} />
          <KeyVal label="Internet probe" value={textOrDash(network.probe)} />
          {network.error ? <KeyVal label="Network detail" value={network.error} /> : null}
          {error ? <Text color="$amber700" sx={{ _dark: { color: '$amber300' } }}>{error}</Text> : null}
        </VStack>
      </Card>
    </Page>
  )
}
