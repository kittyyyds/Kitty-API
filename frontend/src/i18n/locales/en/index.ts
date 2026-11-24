import { mergeLocaleMissing } from '../mergeLocale'
import kittyExtra from './kittyExtra'
import invoice from './invoice'
import billingRules from './billingRules'
import qqbot from './qqbot'
import { homeMessages } from '../../homeOverview'
import providerHub from './providerHub'
import traffic from './traffic'
import connections from './connections'
import requestTiming from './requestTiming'
import channelMonitorV3 from './channelMonitorV3'
import supportTickets from './supportTickets'
import native from './native'
import optimizations from './optimizations'
import coupon from './coupon'
import accountPerformance from './accountPerformance'
import accountTestTiming from './accountTestTiming'
import openaiAuth from './openaiAuth'
import siwc from './siwc'
import modelTrace from './modelTrace'
import qualityOps from './qualityOps'
import adminAccessKeys from './adminAccessKeys'
import landing from './landing'
import common from './common'
import dashboard from './dashboard'
import channelMonitorV2 from './channelMonitorV2'
import admin from './admin'
import misc from './misc'

const base = {
  invoice,
  billingRules,
  ...qqbot,
  homeOverview: homeMessages.en,
  providerHub,
  traffic,
  connections,
  requestTiming,
  coupon,
  accountPerformance,
  accountTestTiming,
  openaiAuth,
  siwc,
  modelTrace,
  qualityOps,
  adminAccessKeys,
  ...landing,
  ...common,
  ...dashboard,
  ...channelMonitorV2,
  ...channelMonitorV3,
  ...supportTickets,
  admin,
  ...misc,
  ...native,
  optimizations,
}

export default mergeLocaleMissing(base, kittyExtra)
