import landing from './landing'
import common from './common'
import dashboard from './dashboard'
import gettingStarted from './gettingStarted'
import channelMonitorV2 from './channelMonitorV2'
import batchImage from './batchImage'
import admin from './admin'
import misc from './misc'
import webchat from './webchat'

export default {
  ...landing,
  ...common,
  ...dashboard,
  ...gettingStarted,
  ...channelMonitorV2,
  ...batchImage,
  admin,
  ...misc,
  ...webchat,
}
