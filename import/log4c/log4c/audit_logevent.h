/****************************************************************************
 *
 * Copyright (c) 2005 Novell, Inc.
 * All Rights Reserved.
 *
 * This program is free software; you can redistribute it and/or
 * modify it under the terms of version 2.1 of the GNU Lesser General Public
 * License as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
 * GNU Lesser General Public License for more details.
 *
 * You should have received a copy of the GNU Lesser General Public License
 * along with this program; if not, contact Novell, Inc.
 *
 * To contact Novell about this file by physical or electronic mail,
 * you may find current contact information at www.novell.com
 *
 ****************************************************************************/

#ifndef _LOGEVENT_H_
#define _LOGEVENT_H_

#ifndef BOOL
#define BOOL int
#endif
#define NAUDIT_EVENT_NETMAIL_UNSPECIFIED 0x0002ffff

/** Wire protocol **/
/* Port */
#define LOGENGINE_PORT            289
#define LOGCACHE_PORT             288
#define LE_PROTOCOL_VERSION       1
#define LE_CACHE_PROTOCOL_VERSION 0x0100474c

/* Commands: */
#define LE_CMD_EVENT          'E'
#define LE_CMD_TERMINATE      'Q'
#define LE_CMD_STARTTLS       'T'
#define LE_CMD_PERFORM_AUTH   'A'
#define LE_CMD_STOPTLS        'N'
#define LE_CMD_ADD_MONITOR    'D'
#define LE_CMD_REMOVE_MONITOR 'X'
#define LE_CMD_RESET_MONITOR  'S'
#define LE_CMD_GET_MONITORS   'M'
#define LE_CMD_WAIT_OK        'W'
#define LE_CMD_GET_CONFIG     'C'
#define LE_CMD_GET_SCHEMA     'G'

/* Responses */
#define LE_CMD_OK     '+'
#define LE_CMD_ERR    '-'
#define LE_CMD_TLS_OK '1'

#define LE_CMD_OK_STRING     "+"
#define LE_CMD_ERR_STRING    "-"
#define LE_CMD_TLS_OK_STRING "1"

/* Flags */
#define LE_FLAG_HAVE_TEXT1  (1<<0)
#define LE_FLAG_HAVE_TEXT2  (1<<1)
#define LE_FLAG_HAVE_DATA   (1<<4)
#define LE_FLAG_SYNCHRONOUS (1<<5)

#define MAX_LOGEVENT_COMPONENT_SIZE 255
#define MAX_LOGEVENT_TEXT1_SIZE     255
#define MAX_LOGEVENT_TEXT2_SIZE     255
#define MAX_LOGEVENT_DATA_SIZE      3072

typedef struct
{
    unsigned long Command;
    unsigned long PayloadSize;
} CmdRequestStruct;

typedef struct _EventStruct
{
    unsigned long Flags;
    unsigned long ComponentSize;
    unsigned long Text1Size;
    unsigned long Text2Size;
    unsigned long DataSize;
    unsigned long ClientTimestamp;
    unsigned long ClientMilliSeconds;
    unsigned long ID;
    unsigned long Severity;
    unsigned long GroupID;
    unsigned long Value1;
    unsigned long Value2;
    unsigned long MIMEHint;
    unsigned char Buffer[MAX_LOGEVENT_COMPONENT_SIZE +
                         MAX_LOGEVENT_TEXT1_SIZE + MAX_LOGEVENT_TEXT2_SIZE +
                         MAX_LOGEVENT_DATA_SIZE];
    unsigned long ServerTimestamp;
    unsigned long SourceIP;
    unsigned char *Description;
    unsigned char *Component;
    unsigned char *Text1;
    unsigned char *Text2;
    unsigned char *Data;
    struct _EventStruct *Next;
} EventStruct;

#define STATIC_EVENT_PART_SIZE (sizeof(Event->Flags)+sizeof(Event->ComponentSize)+sizeof(Event->Text1Size)+sizeof(Event->Text2Size)+sizeof(Event->DataSize)+sizeof(Event->ClientTimestamp)+sizeof(Event->ID)+sizeof(Event->Severity)+sizeof(Event->Value1)+sizeof(Event->Value2)+sizeof(Event->MIMEHint))

typedef struct
{
    unsigned long EventID;
    unsigned long Count;
} MonitorListStruct;

/** Schema **/
/* Classes */
#define NAUDIT_C_ROOT                "NAuditServices"
#define NAUDIT_C_SERVER              "NAuditServer"
#define NAUDIT_C_CHANNEL_CONTAINER   "NAuditChannelContainer"
#define NAUDIT_C_LOGAPP_CONTAINER    "NAuditLogAppContainer"
#define NAUDIT_C_FILTER_CONTAINER    "NAuditFilterContainer"
#define NAUDIT_C_CHANNEL             "NAuditChannel"
#define NAUDIT_C_FILTER              "NAuditFilter"
#define NAUDIT_C_HEARTBEAT           "NAuditHeartbeat"
#define NAUDIT_C_LOGGING_APPLICATION "NAuditLogApp"
#define NAUDIT_C_MYSQL_CHANNEL       "NAuditMySQL"
#define NAUDIT_C_FILE_CHANNEL        "NAuditFile"
#define NAUDIT_C_ORACLE_CHANNEL      "NAuditOracle"
#define NAUDIT_C_SYSLOG_CHANNEL      "NAuditSyslog"
#define NAUDIT_C_SMTP_CHANNEL        "NAuditSMTP"
#define NAUDIT_C_JAVA_CHANNEL        "NAuditJava"
#define NAUDIT_C_CVR_CHANNEL         "NAuditCVR"
#define NAUDIT_C_SNMP_CHANNEL        "NAuditSNMP"

/* Names */
#define NAUDIT_N_ROOT                 "Logging Services"

#define NAUDIT_N_CHANNEL_CONTAINER    "Channels"
#define NAUDIT_N_FILTER_CONTAINER     "Notifications"
#define NAUDIT_N_LOGAPP_CONTAINER     "Applications"

#define NAUDIT_N_FILE_DRIVER          "File"
#define NAUDIT_N_FILE_DRIVER_BINARY   "lgdfile"

#define NAUDIT_N_MYSQL_DRIVER         "MySQL"
#define NAUDIT_N_MYSQL_DRIVER_BINARY  "lgdmsql"

#define NAUDIT_N_ORACLE_DRIVER        "Oracle"
#define NAUDIT_N_ORACLE_DRIVER_BINARY "lgdora"

#define NAUDIT_N_SMTP_DRIVER          "SMTP"
#define NAUDIT_N_SMTP_DRIVER_BINARY   "lgdsmtp"

#define NAUDIT_N_SNMP_DRIVER          "SNMP"
#define NAUDIT_N_SNMP_DRIVER_BINARY   "lgdsnmp"

#define NAUDIT_N_SYSLOG_DRIVER        "Syslog"
#define NAUDIT_N_SYSLOG_DRIVER_BINARY "lgdsyslg"

#define NAUDIT_N_CVR_DRIVER           "Critical Value Reset"
#define NAUDIT_N_CVR_DRIVER_BINARY    "lgdcvr"

#define NAUDIT_N_JAVA_DRIVER          "Java Interface"
#define NAUDIT_N_JAVA_DRIVER_BINARY   "lgdjava"

/* eDir instrumentation driver */
#define NAUDIT_N_EDIR_INSTRUMENTATION "eDirectory Instrumentation"
#define NAUDIT_N_EDIR_APPNAME         "eDirInst"
#define NAUDIT_N_EDIR_APPID           "000B"

/* NetWare instrumentation driver */
#define NAUDIT_N_NW_INSTRUMENTATION "NetWare Instrumentation"
#define NAUDIT_N_NW_APPNAME         "NetWareInst"
#define NAUDIT_N_NW_APPID           "000A"

/* Attributes */
/* Host */
#define NAUDIT_A_LOGGING_SERVER "NAuditLoggingServer"

/* Common */
#define NAUDIT_A_CONFIGURATION   "NAuditConfiguration"
#define NAUDIT_A_INSTRUMENTATION "NAuditInstrumentation"
#define NAUDIT_A_DISABLED        "NAuditDisabled"
#define NAUDIT_A_CONFIG_VERSION  "NAuditConfigVersion"

/* Log Server */
#define NAUDIT_A_HOST                  "NAuditHost"
#define NAUDIT_A_IDENTIFIER            "NAuditIdentifier"
#define NAUDIT_A_EVENTCACHE_MIN        "NAuditEventCacheMin"
#define NAUDIT_A_EVENTCACHE_NORMAL     "NAuditEventCacheNormal"
#define NAUDIT_A_EVENTCACHE_MAX        "NAuditEventCacheMax"
#define NAUDIT_A_EVENTCACHE_RETRIES    "NAuditEventCacheRetries"
#define NAUDIT_A_EVENTCACHE_DELAY      "NAuditEventCacheDelay"
#define NAUDIT_A_EVENTCACHE_BLOCKDELAY "NAuditEventCacheBlockDelay"
#define NAUDIT_A_CHANNEL               "NAuditChannel"
#define NAUDIT_A_DRIVER_DIRECTORY      "NAuditDriverDirectory"
#define NAUDIT_A_SNMP_DESCRIPTION      "NAuditSNMPDescription"
#define NAUDIT_A_SNMP_ORGANIZATION     "NAuditSNMPOrganization"
#define NAUDIT_A_SNMP_LOCATION         "NAuditSNMPLocation"
#define NAUDIT_A_SNMP_CONTACT          "NAuditSNMPContact"
#define NAUDIT_A_SNMP_NAME             "NAuditSNMPName"
#define NAUDIT_A_NLS_DIRECTORY         "NAuditNLSDirectory"
#define NAUDIT_A_BIN_DIRECTORY         "NAuditBinDirectory"
#define NAUDIT_A_TMP_DIRECTORY         "NAuditTmpDirectory"
#define NAUDIT_A_APPCONTAINERS         "NAuditAppContainers"
#define NAUDIT_A_CHANNELCONTAINERS     "NAuditChannelContainers"
#define NAUDIT_A_FILTERCONTAINERS      "NAuditFilterContainers"
#define NAUDIT_A_ANONYMOUS             "NAuditAnonymousPassword"
#define NAUDIT_A_CERTIFICATE           "NAuditCertificate"
#define NAUDIT_A_PRIVATEKEY            "NAuditPrivateKey"

/* Log channel */
#define NAUDIT_A_DRIVER_NAME      "NAuditDriverName"
#define NAUDIT_A_DRIVER_ARGUMENTS "NAuditDriverArguments"
#define NAUDIT_A_DRIVER_STATUS    "NAuditDriverStatus"
#define NAUDIT_A_EXPIRE_CONFIG    "NAuditExpireConfig"

/* Logging application / Logging cache */
#define NAUDIT_A_APPNAME                  "NAuditAppName"
#define NAUDIT_A_APPID                    "NAuditAppID"
#define NAUDIT_A_APP_SCHEMA_BASE          "NAuditAppSchema"
#define NAUDIT_A_APP_SCHEMA_EN            "NAuditAppSchemaEn"
#define NAUDIT_A_APP_SCHEMA_PT            "NAuditAppSchemaPt"
#define NAUDIT_A_APP_SCHEMA_FR            "NAuditAppSchemaFr"
#define NAUDIT_A_APP_SCHEMA_IT            "NAuditAppSchemaIt"
#define NAUDIT_A_APP_SCHEMA_DE            "NAuditAppSchemaDe"
#define NAUDIT_A_APP_SCHEMA_ES            "NAuditAppSchemaEs"
#define NAUDIT_A_APP_SCHEMA_RU            "NAuditAppSchemaRu"

#define NAUDIT_A_CREDENTIALS              "NAuditCredentials"
#define NAUDIT_A_CACHE_DIR                "NAuditCacheDirectory"
#define NAUDIT_A_CACHE_PORT               "NAuditCachePort"
#define NAUDIT_A_CACHE_RECONNECT_INTERVAL "NAuditCacheReconnectInterval"
#define NAUDIT_A_CACHE_SECURE             "NAuditCacheSecure"
#define NAUDIT_A_RESMON_METHOD            "NAuditResMonMethod"
#define NAUDIT_A_RESMON_HOST              "NAuditResMonHost"
#define NAUDIT_A_RESMON_FROM              "NAuditResMonFrom"
#define NAUDIT_A_RESMON_TO                "NAuditResMonTo"
#define NAUDIT_A_RESMON_SUBJECT           "NAuditResMonSubject"
#define NAUDIT_A_RESMON_ID                "NAuditResMonID"
#define NAUDIT_A_RESMON_BODY              "NAuditResMonBody"
#define NAUDIT_A_MONITOR_READ             "NAuditMonitorRead"
#define NAUDIT_A_MONITOR_CONTROL          "NAuditMonitorControl"
#define NAUDIT_A_NOTIFY_CONTROL           "NAuditNotifyControl"

/* Log Filter */
#define NAUDIT_A_FILTER "NAuditFilter"
/*#define NAUDIT_A_CHANNEL "NAuditChannel"*/

/* File driver */
#define NAUDIT_A_FILE_LOGDIR   "NAuditFileLogDir"
#define NAUDIT_A_FILE_MAX_AGE  "NAuditFileMaxAge"
#define NAUDIT_A_FILE_MAX_SIZE "NAuditFileMaxSize"

/* MySQL driver */
#define NAUDIT_A_MYSQL_HOST       "NAuditSQLHost"
#define NAUDIT_A_MYSQL_USER       "NAuditSQLUser"
#define NAUDIT_A_MYSQL_PASSWORD   "NAuditSQLPassword"
#define NAUDIT_A_MYSQL_DATABASE   "NAuditSQLDatabase"
#define NAUDIT_A_MYSQL_TABLE      "NAuditSQLTable"
#define NAUDIT_A_MYSQL_CREATE_SQL "NAuditSQLCreateSQL"
#define NAUDIT_A_MYSQL_EXPIRE_SQL "NAuditSQLExpireSQL"

/* Oracle driver */
#define NAUDIT_A_ORACLE_HOST       NAUDIT_A_MYSQL_HOST
#define NAUDIT_A_ORACLE_USER       NAUDIT_A_MYSQL_USER
#define NAUDIT_A_ORACLE_PASSWORD   NAUDIT_A_MYSQL_PASSWORD
#define NAUDIT_A_ORACLE_DATABASE   NAUDIT_A_MYSQL_DATABASE
#define NAUDIT_A_ORACLE_TABLE      NAUDIT_A_MYSQL_TABLE
#define NAUDIT_A_ORACLE_CREATE_SQL NAUDIT_A_MYSQL_CREATE_SQL
#define NAUDIT_A_ORACLE_EXPIRE_SQL NAUDIT_A_MYSQL_EXPIRE_SQL

/* SMTP driver */
#define NAUDIT_A_SMTP_OPTIONS   "NAuditSMTPOptions"
#define NAUDIT_A_SMTP_HOST      "NAuditSMTPHost"
#define NAUDIT_A_SMTP_USER      "NAuditSMTPUser"
#define NAUDIT_A_SMTP_PASSWORD  "NAuditSMTPPassword"
#define NAUDIT_A_SMTP_SENDER    "NAuditSMTPSender"
#define NAUDIT_A_SMTP_RECIPIENT "NAuditSMTPRecipient"
#define NAUDIT_A_SMTP_SUBJECT   "NAuditSMTPSubject"
#define NAUDIT_A_SMTP_MESSAGE   "NAuditSMTPMessage"

/* Syslog driver */
#define NAUDIT_A_SYSLOG_FACILITY "NAuditSyslogFacility"

/* Critical Value Reset driver */
#define NAUDIT_A_CVR_OBJECT      "NAuditCVRObject"
#define NAUDIT_A_CVR_CREDENTIALS "NAuditCVRCredentials"
#define NAUDIT_A_CVR_RESETLIST   "NAuditCVRResetList"

/* Java driver */
#define NAUDIT_A_JAVA_PATH   "NAuditJavaPath"
#define NAUDIT_A_JAVA_DRIVER "NAuditJavaDriver"

/* SNMP driver */
#define NAUDIT_A_SNMP_TARGET    "NAuditSNMPTarget"
#define NAUDIT_A_SNMP_TRAPOID   "NAuditSNMPTrapOID"
#define NAUDIT_A_SNMP_MESSAGE   "NAuditSNMPMessage"
#define NAUDIT_A_SNMP_COMMUNITY "NAuditSNMPCommunity"


/** Server API **/

/* Log drivers */
typedef struct
{
    void *AuthHandle;
    unsigned char ConfigObject[1024];
    unsigned char ErrorText[256];
    void *DriverData;
} DriverInitStruct;


typedef BOOL (*DriverInitFunc) (DriverInitStruct * LDI);
typedef BOOL (*DriverShutdownFunc) (void *DriverData);
typedef BOOL (*DriverProcessEventFunc) (EventStruct * Event,
                                        void *DriverData);


/** Client API **/

#ifndef LOGEVENT_INTERNAL
typedef void *LOGHANDLE;
#endif

#define LE_EMERGENCY 1
#define LE_ALERT     2
#define LE_CRITICAL  3
#define LE_ERROR     4
#define LE_WARNING   5
#define LE_NOTICE    6
#define LE_INFO      7
#define LE_DEBUG     8

/* Compatibility to syslog; assumes we're included after syslog */
#if !defined(LOG_ERR)
#define LOG_EMERG     1
#define LOG_EMERGENCY 1
#define LOG_ALERT     2
#define LOG_CRIT      3
#define LOG_CRITICAL  3
#define LOG_ERR       4
#define LOG_ERROR     4
#define LOG_WARN      5
#define LOG_WARNING   5
#define LOG_NOTICE    6
#define LOG_INFO      7
#define LOG_DEBUG     8
#endif

/* LogOpen API Flags */
#define LE_NO_BLOCKING          (1<<0)
#define LE_NO_CACHING           (1<<1)
#define LE_FILE_CREDENTIALS     (1<<2)
#define LE_PASSWORD_CREDENTIALS (1<<3)
#define LE_ASN1_CREDENTIALS     (1<<4)
#define LE_SIGNED_CHAIN         (1<<5)
#define LE_TRUNCATE_OVERSIZED   (1<<6)

typedef struct
{
    unsigned char *Data;
    unsigned long Size;
} LogOpenASN1Struct;

/* LogEvent API Flags */
#define LE_SYNCHRONOUS (1<<0)
#define LE_CONSOLE     (1<<1)

/* Error codes */
#define LE_ERR_STRING_TOO_LONG         1
#define LE_ERR_DATA_TOO_LARGE          2
#define LE_ERR_COMPONENT_TOO_LONG      3
#define LE_ERR_WRONG_SERVER_VERSION    4
#define LE_ERR_BAD_SERVER_CREDENTIALS  5
#define LE_ERR_COMMUNICATION_ERROR     6
#define LE_ERR_BUFFER_TOO_SMALL        7
#define LE_ERR_NOT_ENOUGH_MEMORY       8
#define LE_ERR_LOGSERVER_OFFLINE       9
#define LE_ERR_BAD_INSTANCE            10
#define LE_ERR_NOT_LOGGED_COMM_ERR     11
#define LE_ERR_NOT_LOGGED_REMOTE_ERR   12
#define LE_ERR_LOGGED_TO_CACHE         13
#define LE_ERR_CANNOT_LOAD_CERTIFICATE 14
#define LE_ERR_CANNOT_LOAD_PRIVATEKEY  15
#define LE_ERR_INVALID_CERTIFICATE     16
#define LE_ERR_INVALID_PRIVATEKEY      17
#define LE_ERR_FAILED_TLS_SWITCH       18
#define LE_ERR_FAILED_SSL_INIT         19
#define LE_ERR_FAILED_SSL_HANDSHAKE    20

/* Special Application ID macros */
#define MARK_TRANSIENT(AppID) (AppID | 0x80000000)

/* MIME Types - most common */
#define MIME_UNKNOWN                  0
#define MIME_TEXT_PLAIN               1
#define MIME_TEXT_HTML                2
#define MIME_TEXT_XML                 3
#define MIME_APPLICATION_OCTET_STREAM 4

/* MIME Types - non vnd. */
#define MIME_TEXT_RICHTEXT                    100
#define MIME_TEXT_ENRICHED                    101
#define MIME_TEXT_TAB_SEPARATED_VALUES        102
#define MIME_TEXT_SGML                        103
#define MIME_TEXT_XML_EXTERNAL_PARSED_ENTITY  104
#define MIME_TEXT_RTF                         105
#define MIME_TEXT_T140                        106
#define MIME_TEXT_DIRECTORY                   107
#define MIME_TEXT_CALENDAR                    108
#define MIME_MESSAGE_RFC822                   200
#define MIME_MESSAGE_PARTIAL                  201
#define MIME_MESSAGE_EXTERNAL_BODY            202
#define MIME_MESSAGE_NEWS                     203
#define MIME_MESSAGE_HTTP                     204
#define MIME_MESSAGE_DELIVERY_STATUS          205
#define MIME_MESSAGE_DISPOSITION_NOTIFICATION 206
#define MIME_MESSAGE_S_HTTP                   207
#define MIME_APPLICATION_POSTSCRIPT           300
#define MIME_APPLICATION_MSWORD               301
#define MIME_APPLICATION_PDF                  302
#define MIME_APPLICATION_ZIP                  303
#define MIME_APPLICATION_SGML                 304
#define MIME_APPLICATION_PGP_KEYS             305
#define MIME_IMAGE_JPEG                       400
#define MIME_IMAGE_GIF                        401
#define MIME_IMAGE_TIFF                       402
#define MIME_IMAGE_G3FAX                      403
#define MIME_IMAGE_CGM                        404
#define MIME_IMAGE_PNG                        405
#define MIME_AUDIO_BASIC                      500
#define MIME_AUDIO_MPEG                       501
#define MIME_AUDIO_32KADPCM                   502
#define MIME_AUDIO_L16                        503
#define MIME_AUDIO_TONE                       504
#define MIME_AUDIO_PARITYFEC                  505
#define MIME_AUDIO_MP4A_LATM                  506
#define MIME_AUDIO_G_722_1                    507
#define MIME_AUDIO_MPA_ROBUST                 508
#define MIME_AUDIO_L20                        509
#define MIME_AUDIO_L24                        510
#define MIME_VIDEO_MPEG                       600
#define MIME_VIDEO_QUICKTIME                  601
#define MIME_VIDEO_POINTER                    602
#define MIME_VIDEO_PARITYFEC                  603

/* MIME Types - X- */
#define MIME_APPLICATION_X_NETADDRESS 30000



/* "Fake" prototypes - macros */
#define LogEventText(Handle, Component, LogID, LogLevel, Flags, Name1, Name2)  LogEventDirect(Handle, Component, LogID, LogLevel, 0, Flags, Name1, Name2, 0, 0, 0, 0, NULL)
#define LogEventNameValue(Handle, Component, LogID, LogLevel, Flags, Name, Value) LogEventDirect(Handle, Component, LogID, LogLevel, 0, Flags, Name, NULL, Value, 0, 0, 0, NULL)
#define LogEventLong(Handle, Component, LogID, LogLevel, Flags, Value1, Value2) LogEventDirect(Handle, Component, LogID, LogLevel, 0, Flags, NULL, NULL, Value1, Value2, 0, 0, NULL)
#define LogEventRaw(Handle, Component, LogID, LogLevel, Flags, MIMEHint, Size, Data) LogEventDirect(Handle, Component, LogID, LogLevel, 0, Flags, NULL, NULL, 0, 0, MIMEHint, Size, Data)

/* Fun with macros */
#if defined(WIN32)
#    define LEEXPORT(Ret) __declspec(dllexport) Ret __stdcall
#else
#    define LEEXPORT(Ret) Ret
#endif

/* Prototypes */
LEEXPORT (LOGHANDLE) LogOpen (unsigned char *Cert, unsigned char *PKey,
                              unsigned long Flags, unsigned long *Error);
LEEXPORT (LOGHANDLE) LogOpenSimple (unsigned char *AppName,
                                    unsigned char *Password,
                                    unsigned long Flags,
                                    unsigned long *Error);
LEEXPORT (void)
LogClose (LOGHANDLE Handle);
LEEXPORT (unsigned long)
LogGetError (LOGHANDLE Handle);

/* Logevent replaces "syslog()" */
LEEXPORT (BOOL) LogEvent (LOGHANDLE Handle, unsigned char *Component,
                          unsigned long ID, unsigned long Level,
                          unsigned long Flags, unsigned char *Message, ...);

/* Fastest */
LEEXPORT (BOOL) LogEventDirect (LOGHANDLE Handle, unsigned char *Component,
                                unsigned long ID, unsigned long Level,
                                unsigned long Grouping, unsigned long Flags,
                                unsigned char *Text1, unsigned char *Text2,
                                unsigned long Value1, unsigned long Value2,
                                unsigned long MIMEHint,
                                unsigned long DataSize, void *Data);

/* "Advanced" API */
LEEXPORT (BOOL) LogAddMonitor (LOGHANDLE Handle, unsigned long EventID);
LEEXPORT (BOOL) LogRemoveMonitor (LOGHANDLE Handle, unsigned long EventID);
LEEXPORT (BOOL) LogRemoveMonitorList (LOGHANDLE Handle);
LEEXPORT (BOOL) LogRequestMonitors (LOGHANDLE Handle,
                                    unsigned long *ListCount,
                                    MonitorListStruct * MonitorList);

#endif /* _LOGEVENT_H_ */
