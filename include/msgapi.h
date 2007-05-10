/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2001 Novell, Inc. All Rights Reserved.
 * 
 * This program is free software; you can redistribute it and/or
 * modify it under the terms of version 2 of the GNU General Public License
 * as published by the Free Software Foundation.
 * 
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 * 
 * You should have received a copy of the GNU General Public License
 * along with this program; if not, contact Novell, Inc.
 * 
 * To contact Novell about this file by physical or electronic mail, you 
 * may find current contact information at www.novell.com.
 * </Novell-copyright>
 ****************************************************************************/

#ifndef MSGAPI_H
#define MSGAPI_H

#include <mdb.h>
#include <connio.h>

#include <msgftrs.h>
#include <msgdate.h>

#define MSGSRV_LOCAL_SERVER                 NULL
#define MSGSRV_SELECTED_CONTEXT             "."

#define MSGSRV_ROOT                         "Bongo Services"

#define MSGSRV_LIST_ROOT                    "Mailing Lists"
#define MSGSRV_PARENT_ROOT                  "Communities"
#define MSGSRV_TEMPLATE_ROOT                "Templates"
#define MSGSRV_USER_SETTINGS_ROOT           "User Settings"

#define MSGSRV_C_ROOT                       "BongoServices"
#define MSGSRV_C_SERVER                     "BongoMessagingServer"
#define MSGSRV_C_WEBADMIN                   "BongoWebAdminServer"
#define MSGSRV_C_ADDRESSBOOK                "BongoAddressbookAgent"
#define MSGSRV_C_AGENT                      "BongoAgent"
#define MSGSRV_C_ALIAS                      "BongoAliasAgent"
#define MSGSRV_C_ANTISPAM                   "BongoAntiSpamAgent"
#define MSGSRV_C_FINGER                     "BongoFingerAgent"
#define MSGSRV_C_GATEKEEPER                 "BongoConnMgrAgent"
#define MSGSRV_C_CONNMGR                    "BongoConnMgrAgent"
#define MSGSRV_C_IMAP                       "BongoIMAPAgent"
#define MSGSRV_C_LIST                       "BongoList"
#define MSGSRV_C_NDSLIST                    "BongoNDSList"
#define MSGSRV_C_LISTCONTAINER              "BongoListContainer"
#define MSGSRV_C_LISTUSER                   "BongoListUser"
#define MSGSRV_C_LISTAGENT                  "BongoListAgent"
#define MSGSRV_C_STORE                      "BongoStoreAgent"
#define MSGSRV_C_QUEUE                      "BongoQueueAgent"
#define MSGSRV_C_POP                        "BongoPOPAgent"
#define MSGSRV_C_PROXY                      "BongoProxyAgent"
#define MSGSRV_C_RULESRV                    "BongoRuleAgent"
#define MSGSRV_C_SIGNUP                     "BongoSignupAgent"
#define MSGSRV_C_SMTP                       "BongoSMTPAgent"
#define MSGSRV_C_PARENTCONTAINER            "BongoCommunityContainer"
#define MSGSRV_C_PARENTOBJECT               "BongoCommunity"
#define MSGSRV_C_TEMPLATECONTAINER          "BongoTemplateContainer"
#define MSGSRV_C_ITIP                       "BongoItipAgent"
#define MSGSRV_C_ANTIVIRUS                  "BongoAntiVirusAgent"
#define MSGSRV_C_RESOURCE                   "BongoResource"
#define MSGSRV_C_CONNMGR_MODULE             "BongoConnMgrModule"
#define MSGSRV_C_CM_USER_MODULE             "BongoCMUserModule"
#define MSGSRV_C_CM_LISTS_MODULE            "BongoCMListsModule"
#define MSGSRV_C_CM_RBL_MODULE              "BongoCMRBLModule"
#define MSGSRV_C_CM_RDNS_MODULE             "BongoCMRDNSModule"
#define MSGSRV_C_CALCMD                     "BongoCalCmdAgent"
#define MSGSRV_C_COLLECTOR                  "BongoCollectorAgent"

/* TODO: Remove MSGSRV_C_USER 
   This has been deprecated in favor of C_USER with MSGSRV_C_USER_SETTINGS */
#define MSGSRV_C_USER                       "BongoUser"

#define MSGSRV_C_USER_SETTINGS              "BongoUserSettings"
#define MSGSRV_C_USER_SETTINGS_CONTAINER    "BongoUserSettingsContainer"
#define MSGSRV_C_ORGANIZATIONAL_ROLE        "BongoOrganizationalRole"
#define MSGSRV_C_ORGANIZATION               "BongoOrganization"
#define MSGSRV_C_ORGANIZATIONAL_UNIT        "BongoOrganizationalUnit"
#define MSGSRV_C_GROUP                      "BongoGroup"

#define MSGSRV_NLM_ADDRESSBOOK              "BONGOMSGLDAP.NLM"
#define MSGSRV_NLM_ALIAS                    "BONGOMSGALIAS.NLM"
#define MSGSRV_NLM_ANTISPAM                 "BONGOANTISPAM.NLM"
#define MSGSRV_NLM_FINGER                   "BONGOFINGER.NLM"
#define MSGSRV_NLM_GATEKEEPER               "BONGOGKEEPER.NLM"
#define MSGSRV_NLM_CALCMD                   "BONGOCALCMD.NLM"
#define MSGSRV_NLM_CONNMGR                  "BONGOCONNMGR.NLM"
#define MSGSRV_NLM_IMAP                     "BONGOIMAP.NLM"
#define MSGSRV_NLM_LISTAGENT                "BONGOIMSLIST.NLM"
#define MSGSRV_NLM_STORE                    "BONGOSTORE.NLM"
#define MSGSRV_NLM_QUEUE                    "BONGOQUEUE.NLM"
#define MSGSRV_NLM_POP                      "BONGOPOP3.NLM"
#define MSGSRV_NLM_PROXY                    "BONGOMAILPROX.NLM"
#define MSGSRV_NLM_RULESRV                  "BONGORULES.NLM"
#define MSGSRV_NLM_SIGNUP                   "BONGOMSGSGNUP.NLM"
#define MSGSRV_NLM_SMTP                     "BONGOSMTP.NLM"
#define MSGSRV_NLM_ITIP                     "BONGOITIP.NLM"
#define MSGSRV_NLM_ANTIVIRUS                "BONGOAVIRUS.NLM"
#define MSGSRV_NLM_DMC                      "BONGODMC.NLM"
#define MSGSRV_NLM_CMUSER                   "BONGOCMUSER.NLM"
#define MSGSRV_NLM_CMLISTS                  "BONGOCMLISTS.NLM"
#define MSGSRV_NLM_CMRBL                    "BONGOCMRBL.NLM"
#define MSGSRV_NLM_CMRDNS                   "BONGOCMRDNS.NLM"
#define MSGSRV_NLM_WEBADMIN                 "BONGOWEBADMIN.NLM" 

#define MSGSRV_AGENT_ADDRESSBOOK            "Address Book Agent"
#define MSGSRV_AGENT_ALIAS                  "Alias Agent"
#define MSGSRV_AGENT_ANTISPAM               "AntiSpam Agent"
#define MSGSRV_AGENT_FINGER                 "Finger Agent"
#define MSGSRV_AGENT_GATEKEEPER             "Connection Manager"
#define MSGSRV_AGENT_CALCMD                 "Calendar Text Command Agent"
#define MSGSRV_AGENT_CONNMGR                "Connection Manager"
#define MSGSRV_AGENT_IMAP                   "IMAP Agent"
#define MSGSRV_AGENT_LIST                   "List Agent"
#define MSGSRV_AGENT_STORE                  "Store Agent"
#define MSGSRV_AGENT_QUEUE                  "Queue Agent"
#define MSGSRV_AGENT_POP                    "POP Agent"
#define MSGSRV_AGENT_PROXY                  "Proxy Agent"
#define MSGSRV_AGENT_RULESRV                "Rule Agent"
#define MSGSRV_AGENT_SIGNUP                 "Signup Agent"
#define MSGSRV_AGENT_SMTP                   "SMTP Agent"
#define MSGSRV_AGENT_ITIP                   "Itip Agent"
#define MSGSRV_AGENT_ANTIVIRUS              "AntiVirus Agent"
#define MSGSRV_AGENT_DMC                    "DMC Agent"
#define MSGSRV_AGENT_CMUSER                 "User Module"
#define MSGSRV_AGENT_CMLISTS                "Lists Module"
#define MSGSRV_AGENT_CMRBL                  "RBL Module"
#define MSGSRV_AGENT_CMRDNS                 "RDNS Module"
#define MSGSRV_AGENT_COLLECTOR              "Collector Agent"

#define MSGSRV_A_AGENT_STATUS               "BongoAgentStatus"
#define MSGSRV_A_ALIAS                      "BongoAlias"
#define MSGSRV_A_AUTHENTICATION_REQUIRED    "BongoAuthenticationRequired"
#define MSGSRV_A_CONFIGURATION              "BongoConfiguration"
#define MSGSRV_A_CLIENT                     "BongoClient"
#define MSGSRV_A_CONTEXT                    "BongoContext"
#define MSGSRV_A_DOMAIN                     "BongoDomain"
#define MSGSRV_A_EMAIL_ADDRESS              "BongoEMailAddress"
#define MSGSRV_A_DISABLED                   "BongoDisabled"
#define MSGSRV_A_FINGER_MESSAGE             "BongoFingerMessage"
#define MSGSRV_A_HOST                       "BongoHost"
#define MSGSRV_A_IP_ADDRESS                 "BongoIPAddress"
#define MSGSRV_A_LDAP_SERVER                "BongoLDAPServer"
#define MSGSRV_A_LDAP_NMAP_SERVER           "BongoLDAPNMAPServer"
#define MSGSRV_A_LDAP_SEARCH_DN             "BongoLDAPSearchDN"
#define MSGSRV_A_MESSAGE_LIMIT              "BongoMessageLimit"
#define MSGSRV_A_MESSAGING_SERVER           "BongoMessagingServerDN"
#define MSGSRV_A_MODULE_NAME                "BongoModuleName"
#define MSGSRV_A_MODULE_VERSION             "BongoModuleVersion"
#define MSGSRV_A_MONITORED_QUEUE            "BongoMonitoredQueue"
#define MSGSRV_A_QUEUE_SERVER               "BongoQueueServer"
#define MSGSRV_A_STORE_SERVER               "BongoStoreServer"
#define MSGSRV_A_OFFICIAL_NAME              "BongoOfficialName"
#define MSGSRV_A_QUOTA_MESSAGE              "BongoQuotaMessage"
#define MSGSRV_A_POSTMASTER                 "BongoPostmaster"
#define MSGSRV_A_REPLY_MESSAGE              "BongoReplyMessage"
#define MSGSRV_A_URL                        "BongoURL"
#define MSGSRV_A_RESOLVER                   "BongoResolver"
#define MSGSRV_A_ROUTING                    "BongoRouting"
#define MSGSRV_A_SERVER_STATUS              "BongoServerStatus"
#define MSGSRV_A_QUEUE_INTERVAL             "BongoQueueInterval"
#define MSGSRV_A_QUEUE_TIMEOUT              "BongoQueueTimeout"
#define MSGSRV_A_UID                        "BongoUID"
#define MSGSRV_A_VACATION_MESSAGE           "BongoVacationMessage"
#define MSGSRV_A_WORD                       "BongoWord"
#define MSGSRV_A_MESSAGE_STORE              "BongoMessageStore"
#define MSGSRV_A_FORWARDING_ADDRESS         "BongoForwardingAddress"
#define MSGSRV_A_FORWARDING_ENABLED         "BongoForwardingEnabled"
#define MSGSRV_A_AUTOREPLY_ENABLED          "BongoVacationMessageEnabled"
#define MSGSRV_A_AUTOREPLY_MESSAGE          "BongoReplyMessage"
#define MSGSRV_A_PORT                       "BongoPort"
#define MSGSRV_A_SSL_PORT                   "BongoSSLPort"
#define MSGSRV_A_SNMP_DESCRIPTION           "BongoSNMPDescription"
#define MSGSRV_A_SNMP_VERSION               "BongoSNMPVersion"
#define MSGSRV_A_SNMP_ORGANIZATION          "BongoSNMPOrganization"
#define MSGSRV_A_SNMP_LOCATION              "BongoSNMPLocation"
#define MSGSRV_A_SNMP_CONTACT               "BongoSNMPContact"
#define MSGSRV_A_SNMP_NAME                  "BongoSNMPName"
#define MSGSRV_A_MESSAGING_DISABLED         "BongoMessagingDisabled"
#define MSGSRV_A_STORE_TRUSTED_HOSTS        "BongoStoreTrustedHosts"
#define MSGSRV_A_QUOTA_VALUE                "BongoQuotaValue"
#define MSGSRV_A_SCMS_USER_THRESHOLD        "BongoSCMSUserThreshold"
#define MSGSRV_A_SCMS_SIZE_THRESHOLD        "BongoSCMSSizeThreshold"
#define MSGSRV_A_SMTP_VERIFY_ADDRESS        "BongoSMTPVerifyAddress" 
#define MSGSRV_A_SMTP_ALLOW_AUTH            "BongoSMTPAllowAUTH" 
#define MSGSRV_A_SMTP_ALLOW_VRFY            "BongoSMTPAllowVRFY" 
#define MSGSRV_A_SMTP_ALLOW_EXPN            "BongoSMTPAllowEXPN" 
#define MSGSRV_A_WORK_DIRECTORY             "BongoWorkDirectory"
#define MSGSRV_A_LOGLEVEL                   "BongoLoglevel"
#define MSGSRV_A_MINIMUM_SPACE              "BongoMinimumFreeSpace"
#define MSGSRV_A_SMTP_SEND_ETRN             "BongoSMTPSendETRN"
#define MSGSRV_A_SMTP_ACCEPT_ETRN           "BongoSMTPAcceptETRN"
#define MSGSRV_A_LIMIT_REMOTE_PROCESSING    "BongoLimitRemoteProcessing"
#define MSGSRV_A_LIMIT_REMOTE_START_WD      "BongoLimitRemoteBegWeekday"
#define MSGSRV_A_LIMIT_REMOTE_END_WD        "BongoLimitRemoteEndWeekday"
#define MSGSRV_A_LIMIT_REMOTE_START_WE      "BongoLimitRemoteBegWeekend"
#define MSGSRV_A_LIMIT_REMOTE_END_WE        "BongoLimitRemoteEndWeekend"
#define MSGSRV_A_PRODUCT_VERSION            "BongoProductVersion"
#define MSGSRV_A_PROXY_LIST                 "BongoProxyList"
#define MSGSRV_A_MAXIMUM_ITEMS              "BongoMaximumItems"
#define MSGSRV_A_TIME_INTERVAL              "BongoTimeInterval"
#define MSGSRV_A_RELAYHOST                  "BongoRelayHost"
#define MSGSRV_A_ALIAS_OPTIONS              "BongoAliasOptions"
#define MSGSRV_A_LDAP_OPTIONS               "BongoLDAPOptions"
#define MSGSRV_A_CUSTOM_ALIAS               "BongoCustomAlias"
#define MSGSRV_A_ADVERTISING_CONFIG         "BongoAdvertisingConfig"
#define MSGSRV_A_LANGUAGE                   "BongoLanguage"
#define MSGSRV_A_PREFERENCES                "BongoPreferences"
#define MSGSRV_A_QUOTA_WARNING              "BongoQuotaWarning"
#define MSGSRV_A_QUEUE_TUNING               "BongoQueueTuning"
#define MSGSRV_A_TIMEOUT                    "BongoTimeout"
#define MSGSRV_A_PRIVACY                    "BongoPrivacy"
#define MSGSRV_A_THREAD_LIMIT               "BongoThreadLimit"
#define MSGSRV_A_DBF_PAGESIZE               "BongoDBFPageSize"
#define MSGSRV_A_DBF_KEYSIZE                "BongoDBFKeySize"
#define MSGSRV_A_ADDRESSBOOK_CONFIG         "BongoAddressbookConfig"
#define MSGSRV_A_ADDRESSBOOK_URL_SYSTEM     "BongoAddressbookURLSystem"
#define MSGSRV_A_ADDRESSBOOK_URL_PUBLIC     "BongoAddressbookURLPublic"
#define MSGSRV_A_ADDRESSBOOK                "BongoAddressbook"
#define MSGSRV_A_SERVER_STANDALONE          "BongoServerStandalone"
#define MSGSRV_A_FORWARD_UNDELIVERABLE      "BongoForwardUndeliverable"
#define MSGSRV_A_PHRASE                     "BongoPhrase"
#define MSGSRV_A_ACCOUNTING_ENABLED         "BongoAccountingEnabled"
#define MSGSRV_A_ACCOUNTING_DATA            "BongoAccountingData"
#define MSGSRV_A_BILLING_DATA               "BongoBillingData"
#define MSGSRV_A_BLOCKED_ADDRESS            "BongoBlockedAddress"
#define MSGSRV_A_ALLOWED_ADDRESS            "BongoAllowedAddress"
#define MSGSRV_A_RECIPIENT_LIMIT            "BongoRecipientLimit"
#define MSGSRV_A_RBL_HOST                   "BongoRBLHost"
#define MSGSRV_A_SIGNATURE                  "BongoSignature"
#define MSGSRV_A_CONNMGR                    "BongoConnMgr"
#define MSGSRV_A_CONNMGR_CONFIG             "BongoConnMgrConfig"
#define MSGSRV_A_USER_DOMAIN                "BongoUserDomain"
#define MSGSRV_A_RTS_ANTISPAM_CONFIG        "BongoRTSAntispamConfig"
#define MSGSRV_A_SPOOL_DIRECTORY            "BongoSpoolDirectory"
#define MSGSRV_A_SCMS_DIRECTORY             "BongoSCMSDirectory"
#define MSGSRV_A_STORE_SYSTEM_DIRECTORY     "BongoStoreSystemDirectory"
#define MSGSRV_A_DBF_DIRECTORY              "BongoDBFDirectory"
#define MSGSRV_A_NLS_DIRECTORY              "BongoNLSDirectory"
#define MSGSRV_A_BIN_DIRECTORY              "BongoBinDirectory"
#define MSGSRV_A_LIB_DIRECTORY              "BongoLibDirectory"
#define MSGSRV_A_DEFAULT_CHARSET            "BongoDefaultCharset"
#define MSGSRV_A_RULE                       "BongoRule"
#define MSGSRV_A_RELAY_DOMAIN               "BongoRelayDomain"
#define MSGSRV_A_LIST_DIGESTTIME            "BongoListDigestTime"
#define MSGSRV_A_LIST_ABSTRACT              "BongoListAbstract"
#define MSGSRV_A_LIST_DESCRIPTION           "BongoListDescription"
#define MSGSRV_A_LIST_CONFIGURATION         "BongoListConfiguration"
#define MSGSRV_A_LIST_STORE                 "BongoListStore"
#define MSGSRV_A_LIST_NMAPSTORE             "BongoListNMAPStore"
#define MSGSRV_A_LIST_MODERATOR             "BongoListModerator"
#define MSGSRV_A_LIST_OWNER                 "BongoListOwner"
#define MSGSRV_A_LIST_SIGNATURE             "BongoListSignature"
#define MSGSRV_A_LIST_SIGNATURE_HTML        "BongoListSignatureHTML"
#define MSGSRV_A_LIST_DIGEST_VERSION        "BongoListDigestVersion"
#define MSGSRV_A_LIST_MEMBERS               "BongoListMembers"
#define MSGSRV_A_LISTUSER_OPTIONS           "BongoListUserOptions"
#define MSGSRV_A_LISTUSER_ADMINOPTIONS      "BongoListUserAdminOptions"
#define MSGSRV_A_LISTUSER_PASSWORD          "BongoListUserPassword"
#define MSGSRV_A_FEATURE_SET                "BongoFeatureSet"
#define MSGSRV_A_PRIVATE_KEY_LOCATION       "BongoPKeyFile"
#define MSGSRV_A_CERTIFICATE_LOCATION       "BongoCAFile"
#define MSGSRV_A_CONFIG_CHANGED             "BongoConfigVersion"
#define MSGSRV_A_LISTSERVER_NAME            "BongoListServerName"
#define MSGSRV_A_LIST_WELCOME_MESSAGE       "BongoListWelcomeMessage"
#define MSGSRV_A_PARENT_OBJECT              "BongoCommunityDN"
#define MSGSRV_A_FEATURE_INHERITANCE        "BongoFeatureInheritance"
#define MSGSRV_A_TEMPLATE                   "BongoTemplate"
#define MSGSRV_A_TIMEZONE                   "BongoTimezone"
#define MSGSRV_A_LOCALE                     "BongoLocale"
#define MSGSRV_A_PASSWORD_CONFIGURATION     "BongoPasswordConfig"
#define MSGSRV_A_TITLE                      "BongoTitle"
#define MSGSRV_A_DEFAULT_TEMPLATE           "BongoDefaultTemplate"
#define MSGSRV_A_TOM_MANAGER                "BongoCommunityManager"
#define MSGSRV_A_TOM_CONTEXTS               "BongoCommunityContexts"
#define MSGSRV_A_TOM_DOMAINS                "BongoCommunityDomains"
#define MSGSRV_A_TOM_OPTIONS                "BongoCommunityOptions"
#define MSGSRV_A_DESCRIPTION                "BongoDescription"
#define MSGSRV_A_STATSERVER_1               "BongoStatServer1"
#define MSGSRV_A_STATSERVER_2               "BongoStatServer2"
#define MSGSRV_A_STATSERVER_3               "BongoStatServer3"
#define MSGSRV_A_STATSERVER_4               "BongoStatServer4"
#define MSGSRV_A_STATSERVER_5               "BongoStatServer5"
#define MSGSRV_A_STATSERVER_6               "BongoStatServer6"
#define MSGSRV_A_NEWS                       "BongoNews"
#define MSGSRV_A_MANAGER                    "BongoManager"
#define MSGSRV_A_AVAILABLE_SHARES           "BongoAvailableShares"
#define MSGSRV_A_OWNED_SHARES               "BongoOwnedShares"
#define MSGSRV_A_NEW_SHARE_MESSAGE          "BongoNewShareMessage"
#define MSGSRV_A_AVAILABLE_PROXIES          "BongoAvailableProxies"
#define MSGSRV_A_OWNED_PROXIES              "BongoOwnedProxies"
#define MSGSRV_A_RESOURCE_MANAGER           "BongoManager"
#define MSGSRV_A_BONGO_MESSAGING_SERVER      "BongoMessagingServer"
#define MSGSRV_A_ACL                        "BongoACL"
#define MSGSRV_A_WA_DEFAULT_TEMPLATE        "WebAdminDefaultTemplate"
#define MSGSRV_A_WA_ALLOWED_TEMPLATES       "WebAdminAllowedTemplates"
#define MSGSRV_A_WA_DISALLOWED_TEMPLATES    "WebAdminDisallowedTemplates"
#define MSGSRV_A_SMS_AUTHORIZED_PHONES      "BongoSMSAuthorizedPhones"
#define MSGSRV_A_CALCMD_ADDRESS_SUFFIX      "BongoCalCmdAddressSuffix"
#define MSGSRV_A_SPAMD_ENABLED              "BongoSpamdEnabled"
#define MSGSRV_A_SPAMD_TIMEOUT              "BongoSpamdConnectionTimeout"
#define MSGSRV_A_SPAMD_THRESHOLD_HEAD       "BongoSpamdHeaderThreshold"
#define MSGSRV_A_SPAMD_THRESHOLD_DROP       "BongoSpamdDropThreshold"
#define MSGSRV_A_SPAMD_QUARANTINE_Q         "BongoSpamdQuarantineQueue"
#define MSGSRV_A_SPAMD_HOST                 "BongoSpamdHost"
#define MSGSRV_A_SPAMD_FEEDBACK_HOST        "BongoSpamdFeedbackHost"
#define MSGSRV_A_SPAMD_FEEDBACK_ENABLED     "BongoSpamdFeedbackEnabled"
#define MSGSRV_A_MAX_LOAD                   "BongoMaxLoad"
#define MSGSRV_A_ACL_ENABLED                "BongoACLEnabled"
#define MSGSRV_A_UBE_SMTP_AFTER_POP         "BongoUBESMTPAfterPOP"
#define MSGSRV_A_UBE_REMOTE_AUTH_ONLY       "BongoUBERemoteAuthOnly"
#define MSGSRV_A_MAX_FLOOD_COUNT            "BongoMaxFloodCount"
#define MSGSRV_A_MAX_NULL_SENDER_RECIPS     "BongoMaxNullSenderRecips"
#define MSGSRV_A_MAX_MX_SERVERS             "BongoMaxMXServers"
#define MSGSRV_A_RELAY_LOCAL_MAIL           "BongoRelayLocalMail"
#define MSGSRV_A_CLUSTERED                  "BongoClustered"
#define MSGSRV_A_FORCE_BIND                 "BongoForceBind"
#define MSGSRV_A_SSL_TLS                    "BongoSSLTLS"
#define MSGSRV_A_SSL_ALLOW_CHAINED          "BongoSSLAllowChained"
#define MSGSRV_A_BOUNCE_RETURN              "BongoBounceReturn"
#define MSGSRV_A_BOUNCE_CC_POSTMASTER       "BongoBounceCCPostmaster"
#define MSGSRV_A_QUOTA_USER                 "BongoQuotaUser"
#define MSGSRV_A_QUOTA_SYSTEM               "BongoQuotaSystem"
#define MSGSRV_A_REGISTER_QUEUE             "BongoRegisterQueue"
#define MSGSRV_A_QUEUE_LIMIT_BOUNCES        "BongoQueueLimitBounces"
#define MSGSRV_A_LIMIT_BOUNCE_ENTRIES       "BongoLimitBounceEntries"
#define MSGSRV_A_LIMIT_BOUNCE_INTERVAL      "BongoLimitBounceInterval"
#define MSGSRV_A_DOMAIN_ROUTING             "BongoDomainRouting"
#define MSGSRV_A_MONITOR_INTERVAL           "BongoMonitorInterval"
#define MSGSRV_A_MANAGER_PORT               "BongoManagerPort"
#define MSGSRV_A_MANAGER_SSL_PORT           "BongoManagerSSLPort"

#define PLUSPACK_AGENT                      "PlusPack Agent"
#define PLUSPACK_C                          "PlusPackAgent"
#define PLUSPACK_NLM                        "BONGOPLUSPACK.NLM"
#define PLUSPACK_A_ACL_GROUP                "PlusPackACLGroup"
#define PLUSPACK_A_COPY_USER                "PlusPackCopyUser"
#define PLUSPACK_A_COPY_MBOX                "PlusPackCopyMBOX"

#define MSGSRV_LOAD_AGENT                   1
#define MSGSRV_UNLOAD_AGENT                 2
#define MSGSRV_RESTART_AGENT                3
#define MSGSRV_GET_STATS                    4
#define MSGSRV_RESET_STATS                  5
#define MSGSRV_GET_SPAM_STATS               6

#define MSGSRV_SUCCESS                      1
#define MSGSRV_NOT_AUTHORIZED               2
#define MSGSRV_UNKNOWN_REQUEST              3
#define MSGSRV_WRONG_ARGUMENTS              4

#define MSGSRV_MODULES_LOADED               0   /* Private                                             */
#define MSGSRV_UNAUTHORIZED                 1   /* Increments                                          */
#define MSGSRV_WRONG_PASSWORD               2   /* Increments                                          */
#define MSGSRV_DELIVERY_FAILED_LOCAL        3   /* Increments                                          */
#define MSGSRV_DELIVERY_FAILED_REMOTE       4   /* Increments                                          */

#define MSGSRV_INCOMING_CLIENT              5   /* Increments                                          */
#define MSGSRV_INCOMING_SERVER              6   /* Increments                                          */
#define MSGSRV_SERVICED_CLIENT              7   /* Increments, also decrements MSGSRV_INCOMING_CLIENTS */
#define MSGSRV_SERVICED_SERVER              8   /* Increments, also decrements MSGSRV_INCOMING_SERVERS */
#define MSGSRV_OUTGOING_SERVER              9   /* Increments                                          */
#define MSGSRV_CLOSED_OUT_SERVER            10  /* Increments, also decrements MSGSRV_OUTGOING_SERVERS */

#define MSGSRV_INCOMING_Q_AGENT             11  /* Increments/Decrements                               */
#define MSGSRV_OUTGOING_Q_AGENT             12  /* Increments/Decrements                               */
#define MSGSRV_STORE_AGENT                  13  /* Increments                                          */

#define MSGSRV_MSG_RECEIVED_LOCAL           15  /* Value    +/-                                        */
#define MSGSRV_MSG_RECEIVED_REMOTE          16  /* Value    +/-                                        */
#define MSGSRV_MSG_STORED_LOCAL             17  /* Value    +/-                                        */
#define MSGSRV_MSG_STORED_REMOTE            18  /* Value    +/-                                        */
#define MSGSRV_MSG_QUEUED_LOCAL             19  /* Value +/-                                           */
#define MSGSRV_MSG_QUEUED_REMOTE            20  /* Value +/-                                           */
                    
#define MSGSRV_BYTE_RECEIVED_LOCAL          21  /* Value +/-                                           */
#define MSGSRV_BYTE_RECEIVED_REMOTE         22  /* Value +/-                                           */
#define MSGSRV_BYTE_STORED_LOCAL            23  /* Value +/-                                           */
#define MSGSRV_BYTE_STORED_REMOTE           24  /* Value +/-                                           */

#define MSGSRV_RCPT_RECEIVED_LOCAL          25  /* Value +/-                                           */
#define MSGSRV_RCPT_RECEIVED_REMOTE         26  /* Value +/-                                           */
#define MSGSRV_RCPT_STORED_LOCAL            27  /* Value +/-                                           */
#define MSGSRV_RCPT_STORED_REMOTE           28  /* Value +/-                                           */

#define MSGSRV_SPAM_QUEUESPAM               29  /* Increments                                          */
#define MSGSRV_SPAM_ADDRESS_BLOCKED         30  /* Increments                                          */
#define MSGSRV_SPAM_MAPS_BLOCKED            31  /* Increments                                          */
#define MSGSRV_SPAM_DENIED_ROUTING          32  /* Increments                                          */
#define MSGSRV_SPAM_NO_DNS_ENTRY            33  /* Increments                                          */

#define MSGSRV_NMAP_TO_NMAP_AGENT           34  /* Increments                                          */

#define MSGSRV_SPAM_MESSAGES_SCANNED        35  /* Increments                                          */
#define MSGSRV_SPAM_ATTACHMENTS_SCANNED     36  /* Increments                                          */
#define MSGSRV_SPAM_ATTACHMENTS_BLOCKED     37  /* Increments                                          */
#define MSGSRV_SPAM_VIRUSES_FOUND           38  /* Increments                                          */
#define MSGSRV_SPAM_VIRUSES_BLOCKED         39  /* Increments                                          */
#define MSGSRV_SPAM_VIRUSES_CURED           40  /* Increments                                          */

#define MSGSRV_MAX_STAT_COUNT               41  /* How many stat entries are there?                    */

#define MSGSRV_STAT_UP                      1
#define MSGSRV_STAT_DOWN                    -1

#define MSGSRV_TRAP_ILLEGALACCESS           3
#define MSGSRV_TRAP_OTHER                   4

/* Global configuration file.  See MsgGetConfigProperty() */
#define MSGSRV_CONFIG_FILENAME              "bongo.conf"
#define MSGSRV_CONFIG_MAX_PROP_CHARS         255
#define MSGSRV_CONFIG_PROP_MESSAGING_SERVER "MessagingServer"
#define MSGSRV_CONFIG_PROP_WEB_ADMIN_SERVER "WebAdminServer"
#define MSGSRV_CONFIG_PROP_DEFAULT_CONTEXT  "DefaultContext"
#define MSGSRV_CONFIG_PROP_BONGO_SERVICES    "BongoServices"
#define MSGSRV_CONFIG_PROP_MANAGED_SLAPD    "ManagedSlapd"
#define MSGSRV_CONFIG_PROP_MANAGED_SLAPD_PATH "ManagedSlapdPath"
#define MSGSRV_CONFIG_PROP_MANAGED_SLAPD_PORT "ManagedSlapdPort"
#define MSGSRV_CONFIG_PROP_MANAGED_SLAPD    "ManagedSlapd"

#define MSGFindUser(u, dn, t, n, v)         MsgFindObject((u), (dn), (t), (n), (v)) 

#ifdef __cplusplus
extern "C" {
#endif

typedef BOOL (*FindObjectCacheType)(const unsigned char *, unsigned char *, unsigned char *, struct sockaddr_in *, MDBValueStruct *);
typedef BOOL (*FindObjectCacheExType)(const unsigned char *, unsigned char *, unsigned char *, struct sockaddr_in *, BOOL *, MDBValueStruct *);
typedef const unsigned char *(*FindObjectStoreCacheType)(const unsigned char *User, const unsigned char *DefaultPath);

typedef struct _MSGCacheInitStruct {
    MDBHandle *DirectoryHandle;

    XplRWLock *ConfigLock;

    struct sockaddr_in *ServerAddr;
    MDBValueStruct *ServerContexts;
    MDBValueStruct *StorePath;

    FindObjectCacheType DefaultFindObject;
    FindObjectCacheExType DefaultFindObjectEx;
    FindObjectStoreCacheType DefaultFindObjectStore;

    unsigned char DefaultPathChar;
} MSGCacheInitStruct;

typedef BOOL (*FindObjectCacheInitType)(MSGCacheInitStruct *initData, unsigned char *description);
typedef BOOL (*FindObjectCacheShutdownType)(void);

EXPORT MDBHandle MsgInit(void);
EXPORT BOOL MsgShutdown(void);
EXPORT MDBHandle MsgGetSystemDirectoryHandle(void);

EXPORT const unsigned char *MsgGetServerDN(unsigned char *buffer);
EXPORT MDBHandle MsgDirectoryHandle(void);
EXPORT BOOL MsgGetConfigProperty(unsigned char *Buffer, unsigned char *Property);

EXPORT BOOL MsgFindObject(const unsigned char *name, unsigned char *dn, unsigned char *type, struct sockaddr_in *nmap, MDBValueStruct *v);
EXPORT BOOL MsgFindObjectEx(const unsigned char *name, unsigned char *dn, unsigned char *type, struct sockaddr_in *nmap, BOOL *disabled, MDBValueStruct *v);
EXPORT const unsigned char *MsgFindUserStore(const unsigned char *user, const unsigned char *defaultPath);
EXPORT BOOL MsgFindUserNmap(const unsigned char *user, unsigned char *nmap, int nmap_len, unsigned short *port);

EXPORT BOOL MsgReadIP(const unsigned char *object, unsigned char *type, MDBValueStruct *v);

EXPORT const unsigned char *MsgGetDBFDir(char *directory);
EXPORT const unsigned char *MsgGetWorkDir(char *directory);
EXPORT const unsigned char *MsgGetNLSDir(char *directory);
EXPORT const unsigned char *MsgGetLibDir(char *directory);
EXPORT const unsigned char *MsgGetBinDir(char *directory);
EXPORT const unsigned char *MsgGetTLSCertPath(char *path);
EXPORT const unsigned char *MsgGetTLSKeyPath(char *path);
EXPORT unsigned char *MsgGetUserEmailAddress(const unsigned char *userDn, MDBValueStruct *userData, unsigned char *buffer, unsigned long bufLen);
EXPORT unsigned char *MsgGetUserDisplayName(const unsigned char *userDn, MDBValueStruct *userData);
EXPORT BOOL MsgGetUserSettingsDN(const unsigned char *userDn, unsigned char *configDn, MDBValueStruct *v, BOOL create);
EXPORT long MsgGetUserSetting(const unsigned char *userDn, unsigned char *attribute, MDBValueStruct *vOut);
EXPORT BOOL MsgSetUserSetting(const unsigned char *userDn, unsigned char *attribute, MDBValueStruct *vIn);
EXPORT BOOL MsgAddUserSetting(const unsigned char *userDn, unsigned char *attribute, unsigned char *value, MDBValueStruct *vIn);
EXPORT unsigned long MsgGetHostIPAddress(void);
EXPORT unsigned long MsgGetAgentBindIPAddress(void);

EXPORT const char *MsgGetUnprivilegedUser(void);	

EXPORT void MsgMakePath(unsigned char *path);
EXPORT BOOL MsgCleanPath(unsigned char *path);

EXPORT BOOL MsgResolveStart();
EXPORT BOOL MsgResolveStop();    

EXPORT void MsgGetUid(char *buffer, int buflen);

EXPORT BOOL MsgGetAvailableVersion(int *version);
EXPORT BOOL MsgGetBuildVersion(int *version, BOOL *custom);

EXPORT void MsgNmapChallenge(const unsigned char *response, unsigned char *reply, size_t length);

/* from msgcollector.c */

enum {
    MSG_COLLECT_OK,
    MSG_COLLECT_ERROR_URL = -1,
    MSG_COLLECT_ERROR_DOWNLOAD = -2,
    MSG_COLLECT_ERROR_AUTH = -3,
    MSG_COLLECT_ERROR_PARSE = -4,
    MSG_COLLECT_ERROR_STORE = -5,
};  

EXPORT int MsgCollectUser(const char *user);
EXPORT void MsgCollectAllUsers(void);
EXPORT int MsgIcsImport(const char *user, 
                        const char *calendarName, 
                        const char *calendarColor, 
                        const char *url, 
                        const char *urlUsername, 
                        const char *urlPassword, 
                        uint64_t *guidOut);

EXPORT int MsgIcsSubscribe(const char *user, 
                           const char *calendarName, 
                           const char *calendarColor, 
                           const char *url, 
                           const char *urlUsername, 
                           const char *urlPassword, 
                           uint64_t *guidOut);

EXPORT int MsgImportIcs(FILE *fh, 
                        const char *user, 
                        const char *calendarName, 
                        const char *calendarColor, 
                        Connection *existingConn);

EXPORT int MsgImportIcsUrl(const char *user, 
                           const char *calendarName, 
                           const char *calendarColor, 
                           const char *url, 
                           const char *urlUsername, 
                           const char *urlPassword, 
                           Connection *existingConn);

#ifdef __cplusplus
}
#endif

#endif /* MSGAPI_H */

