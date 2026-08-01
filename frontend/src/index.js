import React from 'react'
import ReactDOM from 'react-dom/client'
import { PluginApp } from '@spr-networks/plugin-ui'

import Plugin from './Plugin'

// Do not use StrictMode here. The SDK's gluestack root provider relies on a
// single render when it establishes the host-provided color mode.
ReactDOM.createRoot(document.getElementById('root')).render(
  <PluginApp>
    <Plugin />
  </PluginApp>
)
