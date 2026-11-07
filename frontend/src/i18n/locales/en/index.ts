import adminAccessKeys from './adminAccessKeys'
import mcp from './mcp'
import landing from './landing'
import common from './common'
import dashboard from './dashboard'
import channelMonitorV2 from './channelMonitorV2'
import channelMonitorV3 from './channelMonitorV3'
import batchImage from './batchImage'
import imagegen from './imagegen'
import groupSignals from './groupSignals'
import videoBilling from './videoBilling'
import admin from './admin'
import misc from './misc'
import tickets from './tickets'
import mediaPricing from './mediaPricing'

export default {
  mediaPricing,
  adminAccessKeys,
  mcp,
  videoBilling,
  ...landing,
  ...common,
  ...dashboard,
  ...channelMonitorV2,
  ...channelMonitorV3,
  ...batchImage,
  ...imagegen,
  ...groupSignals,
  admin,
  ...misc,
  ...tickets,
}
