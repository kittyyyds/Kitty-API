import upstream from './upstream'
import providerImport from './providerImport'
import operations from './operations'
import groupCacheTest from './groupCacheTest'
import overview from './overview'
import channels from './channels'
import accounts from './accounts'
import resources from './resources'
import ops from './ops'
import settings from './settings'
import audit from './audit'
import plugins from './plugins'

export default {
  ...upstream,
  ...providerImport,
  ...operations,
  ...groupCacheTest,
  ...overview,
  ...channels,
  ...accounts,
  ...resources,
  ...ops,
  ...settings,
  ...audit,
  ...plugins,
}
