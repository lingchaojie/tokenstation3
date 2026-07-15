import landing from './landing'
import common from './common'
import dashboard from './dashboard'
import gettingStarted from './gettingStarted'
import admin from './admin'
import misc from './misc'
import webchat from './webchat'

export default {
  ...landing,
  ...common,
  ...dashboard,
  ...gettingStarted,
  admin,
  ...misc,
  ...webchat,
}
