import overview from './overview'
import channels from './channels'
import accounts from './accounts'
import resources from './resources'
import ops from './ops'
import settings from './settings'
import audit from './audit'
import modelPricingSync from './modelPricingSync'
import promptAudit from './promptAudit'
import plugins from './plugins'
import groupCacheTest from './groupCacheTest'
import requestCapture from './requestCapture'
import accountOps from './accountOps'

export default {
  ...overview,
  ...channels,
  ...accounts,
  ...resources,
  ...ops,
  ...settings,
  ...audit,
  ...modelPricingSync,
  ...promptAudit,
  ...plugins,
  ...groupCacheTest,
  ...requestCapture,
  ...accountOps,
}
