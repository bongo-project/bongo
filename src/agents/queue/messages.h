#ifndef STRINGS_H
#define STRINGS_H

#define NMAP_AUTH_COMMAND "AUTH"
#define NMAP_AUTH_HELP "AUTH <Hash> - Authenticates un-trusted hosts using an MD5 hash of the NMAP password.\r\n"

#define NMAP_CONF_COMMAND "CONF"

#define NMAP_HELP_NOT_DEFINED "Help not available.\r\n"

#define NMAP_HELP_COMMAND "HELP"
#define NMAP_HELP_HELP "HELP [Command] - Without an argument, lists all NMAP Commands, otherwise lists basic description of Command\r\n"

#define NMAP_PASS_COMMAND "PASS"
#define NMAP_PASS_HELP "PASS {SYS | <USER <Username>} <Password> - Authenticate as NMAP or Username with cleartext Password.\r\n"

#define NMAP_QUIT_COMMAND "QUIT"
#define NMAP_QUIT_HELP "QUIT - Closes the client's NMAP connection.\r\n"
#define NMAP_PROXY_COMMAND "PROXY"

#define NMAP_QADDM_COMMAND "QADDM"

#define NMAP_QADDQ_COMMAND "QADDQ"

#define NMAP_QABRT_COMMAND "QABRT"
#define NMAP_QABRT_HELP "QABRT - Removes the current message before processing.\r\n"

#define NMAP_QBODY_COMMAND "QBODY"

#define NMAP_QBRAW_COMMAND "QBRAW"

#define NMAP_QCOPY_COMMAND "QCOPY"

#define NMAP_QCREA_COMMAND "QCREA"
#define NMAP_QCREA_HELP "QCREA [<Queue>] - Creates a new queue entry.\r\n"

#define NMAP_QDELE_COMMAND "QDELE"

#define NMAP_QDONE_COMMAND "QDONE"

#define NMAP_QDSPC_COMMAND "QDSPC"
#define NMAP_QDSPC_HELP "QDSPC - Returns the amount of MemFree disk space, in bytes, in the message queue.\r\n"

#define NMAP_QEND_COMMAND "QEND"

#define NMAP_QGREP_COMMAND "QGREP"

#define NMAP_QHEAD_COMMAND "QHEAD"

#define NMAP_QINFO_COMMAND "QINFO"

#define NMAP_QMOD_FROM_COMMAND "QMOD FROM"

#define NMAP_QMOD_FLAGS_COMMAND "QMOD FLAGS"

#define NMAP_QMOD_LOCAL_COMMAND "QMOD LOCAL"

#define NMAP_QMOD_MAILBOX_COMMAND "QMOD MAILBOX"

#define NMAP_QMOD_RAW_COMMAND "QMOD RAW"

#define NMAP_QMOD_TO_COMMAND "QMOD TO"

#define NMAP_QMIME_COMMAND "QMIME"

#define NMAP_QMOVE_COMMAND "QMOVE"

#define NMAP_QRCP_COMMAND "QRCP"

#define NMAP_QRETR_COMMAND "QRETR"

#define NMAP_QRTS_COMMAND "QRTS"

#define NMAP_QRUN_COMMAND "QRUN"
#define NMAP_QRUN_HELP "QRUN [<Queue ID>] - Places a queue entry in the message queue for immediate processing.\r\n"

#define NMAP_QSQL_COMMAND "QSQL"

#define NMAP_QSRCH_DOMAIN_COMMAND "QSRCH DOMAIN"

#define NMAP_QSRCH_HEADER_COMMAND "QSRCH HEADER"

#define NMAP_QSRCH_BODY_COMMAND "QSRCH BODY"

#define NMAP_QSRCH_BRAW_COMMAND "QSRCH BRAW"

#define NMAP_QSTOR_ADDRESS_COMMAND "QSTOR ADDRESS"

#define NMAP_QSTOR_CAL_COMMAND "QSTOR CAL"

#define NMAP_QSTOR_FLAGS_COMMAND "QSTOR FLAGS"

#define NMAP_QSTOR_FROM_COMMAND "QSTOR FROM"

#define NMAP_QSTOR_LOCAL_COMMAND "QSTOR LOCAL"

#define NMAP_QSTOR_MESSAGE_COMMAND "QSTOR MESSAGE"

#define NMAP_QSTOR_RAW_COMMAND "QSTOR RAW"

#define NMAP_QSTOR_TO_COMMAND "QSTOR TO"

#define NMAP_QUIT_COMMAND "QUIT"
#define NMAP_QUIT_HELP "QUIT - Closes the client's NMAP connection.\r\n"

#define NMAP_QWAIT_COMMAND "QWAIT"
#define NMAP_QWAIT_HELP "QWAIT <Queue> <Port> <Identifier> - Register for NMAP message queue.\r\n"

#define NMAP_QFLUSH_COMMAND "QFLUSH"
#define NMAP_QFLUSH_HELP "QFLUSH - Attempt to deliver all queued mail.\r\n"

/* --------------------------------------------------- */
#define NMAP_ADDRESS_RESOLVE_COMMAND "ADDRESS RESOLVE"
#define NMAP_ADDRESS_RESOLVE_HELP "ADDRESS RESOLVE - Determine if an email address is local or remote and resolve any aliasing.\r\n"

#define MSG1000LOCAL "1000 %s LOCAL\r\n"
#define MSG1001RELAY "1001 %s RELAY\r\n"
#define MSG1002REMOTE "1002 %s REMOTE\r\n"
/* --------------------------------------------------- */

#define MSG1000READY "Bongo NMAP server ready"
#define MSG1000BYE "Bongo NMAP signing off"
#define MSG1000LOGINOK "1000 Login ok\r\n"
#define MSG1000FLAGSET "Flags set"
#define MSG1000FLAGINFO "Current Flags"
#define MSG1000OK "1000 OK\r\n"
#define MSG1000DELETED "Deleted message"
#define MSG1000ENTRYRMVD "1000 Queue entry removed\r\n"
#define MSG1000ENTRYMADE "1000 Queue entry created\r\n"
#define MSG1000SPACE_AVAIL "1000 %lu bytes free\r\n"
#define MSG1000USER "1000 User selected\r\n"
#define MSG1000MBOX "Mailbox selected [Read-Write]"
#define MSG1020MBOX "Mailbox selected [Read-Only]"
#define MSG1000ADBK "Address Book selected [Read-Write]"
#define MSG1020ADBK "Address Book selected [Read-Only]"
#define MSG1000MBOXMADE "1000 Mailbox created\r\n"
#define MSG1000CALENDARMADE "1000 Calendar created\r\n"
#define MSG1000ADBKMADE "1000 Address book created\r\n"
#define MSG1000EQUE "1000 Queue state entered\r\n"
#define MSG1000NOTIMP "1000 Command not implemented\r\n"
#define MSG1000PARAMSET "1000 Parameters set\r\n"
#define MSG1000FEATUREAVAIL "1000 Feature available\r\n"
#define MSG1000PURGED "Message(s) purged"
#define MSG1000SALVAGED "Message(s) salvaged"
#define MSG1000QWATCHMODE "1000 Entering queue watch mode\r\n"
#define MSG1000RQWATCHMODE "1000 Returning to queue watch mode\r\n"
#define MSG1000STARTTLS "1000 Begin TLS negotiations\r\n"
#define MSG1000STORED "1000 %s Created\r\n"
#define MSG2001CAPA "2001-NMAP1 NMAP Protocol Version 1\r\n2001-NMAP2 NMAP Protocol Version 2\r\n2001-ULIST Novonyx Userlist Extension\r\n2001-SLIST Novonyx Serverlist Extension\r\n2001-ADBK Address Book Extension\r\n2001-FEAT Feature Lookup Extension\r\n"
#define MSG2001CAPATLS "2001-TLS Encryption Extension\r\n"
#define MSG2001HELP "2001-------------------------------------------------------------------------\r\n" \
                    "2001-Open Client Commands                                                    \r\n" \
                    "2001-CAPA      ERRO      HELP      NOOP      NVER      OPER      QUIT        \r\n2001-\r\n" \
                    "2001-Authentication Commands                                                 \r\n" \
                    "2001-AUTH      PASS                                                          \r\n2001-\r\n" \
                    "2001-Client Commands - Any Session State                                     \r\n" \
                    "2001-FLAG      RSET                                                          \r\n2001-\r\n" \
                    "2001-Client Commands - Authenticated State                                   \r\n" \
                    "2001-CLR       FDUMP     NAMESPACE QABRT     QCREA     QDSPC     QRUN        \r\n" \
                    "2001-QSTOR     QWAIT     USER      VRFY                                      \r\n2001-\r\n" \
                    "2001-Client Commands - User State                                            \r\n" \
                    "2001-ADBK      CAL       CHECK     CREA      FEAT      MBOX      PROXY       \r\n" \
                    "2001-RESOURCE  RMOV      RNAM      RIGHTS    SHARE     SHOW      SHOWPROXY   \r\n" \
                    "2001-SHOWSH    SHOWSUB   SINFO     SPACE     STOR      STRAW     SUBS        \r\n" \
                    "2001-UNSUBS                                                                  \r\n2001-\r\n" \
                    "2001-Client Commands - Mailbox State                                         \r\n" \
                    "2001-AFLG      AINFO     BODY      BRAW      COPY      DELE      DFLG        \r\n" \
                    "2001-GFLG      GINFO     HEAD      INFO      LIST      MIME      PRGV        \r\n" \
                    "2001-PRGM      PURG      REPAIR    SALV      SEARCH    SFLG      STAT        \r\n" \
                    "2001-UPDA                                                                    \r\n2001-\r\n" \
                    "2001-Client Commands - Calendar State                                        \r\n" \
                    "2001-CSAFLG    CSATND    CSCOMP    CSCREA    CSDELE    CSDFLG    CSFILT      \r\n" \
                    "2001-CSFIND    CSGFLG    CSINFO    CSLIST    CSPRGV    CSPURG    CSRMOV      \r\n" \
                    "2001-CSRNAM    CSSALV    CSSFLG    CSSHOW    CSSTAT    CSSTOR    CSUPDA      \r\n" \
                    "2001-CSOPEN    CSORG     CSFIND    CSCHECK   CSCOPY    CSUNSUBS  CSSHOWSUB   \r\n" \
                    "2001-CSSUBS    CSSINFO   CSSTRAW   CSPRGM                                    \r\n2001-\r\n" \
                    "2001-Client Commands - Callback State                                        \r\n" \
                    "2001-QADDM     QADDQ     QBODY     QBRAW     QCOPY     QDONE     QGREP       \r\n" \
                    "2001-QHEAD     QINFO     QMIME     QMOD      QMOVE     QRCP      QRETR       \r\n" \
                    "2001-QRTS      QSRCH     QDELE     QEND                                      \r\n2001-\r\n" \
                    "2001-Extension Commands                                                      \r\n" \
                    "2001-CONF      ULIST                                                         \r\n" \
                    "2001-------------------------------------------------------------------------\r\n\r\n"
#define MSG2002DELETED "Message(s) have been marked deleted"
#define MSG2002PURGEC "Message(s) to purge"
#define MSG2003MULTIEND "2003-Multipart End\r\n"
#define MSG2004RFC822END "2004-RFC822 End\r\n"
#define MSG2005FEATURENOTAVAIL "2005-Feature not available\r\n"
#define MSG3000UNKNOWN "3000 Unknown command\r\n"
#define MSG3010BADARGC "3010 Wrong number of arguments\r\n"
#define MSG3011BADGUIDLEN "3011 Incorrect GUID length\r\n"
#define MSG3012BADQSTATE "3012 Wrong state; not waiting on queues\r\n"
#define MSG3013BADQSTATE "3013 Wrong state; waiting on queues\r\n"
#define MSG3014REQNOTALLOWED "3014 Request not allowed!%s\r\n"
#define MSG3240NOAUTH "3240 I don't know who you are. AUTH first!\r\n"
#define MSG3240BADSTATE "3240 Command issued in wrong state\r\n"
#define MSG3241NOUSER "3241 No user selected. USER first!\r\n"
#define MSG3242BADAUTH "3242 Bad authentication!\r\n"
#define MSG4000OUTOFRANGE "4000 Number out of range\r\n"
#define MSG4000CANTUNLOCKENTRY "4000 Queue entry unknown or already being processed\r\n"
#define MSG4120USERLOCKED "4120 User currently locked, try later!\r\n"
#define MSG4120BOXLOCKED "4120 Mailbox currently locked, try later!\r\n"
#define MSG4120CALLOCKED "4120 Calendar currently locked, try later!\r\n"
#define MSG4120SQLLOCKED "4120 SQL currently locked, try later!\r\n"
#define MSG4120ADBKLOCKED "4120 Mailbox currently locked, try later!\r\n"
#define MSG4220NOMSG "4220 No message with that ID\r\n"
#define MSG4220NOCONTACT "4220 No contact with that ID\r\n"
#define MSG4220NOENTRY "4220 No entry with that ID\r\n"
#define MSG4220NOGUID "4220 No document with that GUID\r\n"
#define MSG4220USERSHARES "4220 User owns shared resources.\r\n"
#define MSG4221MBOXDONE "4221 Mailbox already selected. RSET first!\r\n"
#define MSG4221CALDONE "4221 Calendar already selected. RSET first!\r\n"
#define MSG4221ADBKDONE "4221 Address Book already selected. RSET first!\r\n"
#define MSG4221USERDONE "4221 User already selected. RSET first!\r\n"
#define MSG4221MBOXLOCKED "4221 Mailbox currently locked - try later!\r\n"
#define MSG4221ADBKLOCKED "4221 Address Book currently locked - try later!\r\n"
#define MSG4221MBOXSHARED "4221 Mailbox is shared\r\n"
#define MSG4221CALSHARED "4221 Calendar is shared\r\n"
#define MSG4221ADBKSHARED "4221 Calendar is shared\r\n"
#define MSG4222MSGDELETED "4222 Message marked deleted\r\n"
#define MSG4222CONTACTDELETED "4222 Contact marked deleted\r\n"
#define MSG4223MSGDELETED "4223 Message not marked deleted\r\n"
#define MSG4224NOENTRY "4224 Queue entry does not exist\r\n"
#define MSG4224CANTREAD "4224 Can't read queue data\r\n"
#define MSG4224CANTWRITE "4224 Can't write queue data\r\n"
#define MSG4224NOSTORE "4224 User store doesn't exist\r\n"
#define MSG4224NOMBOX "4224 Mailbox doesn't exist\r\n"
#define MSG4224NOCSTORE "4224 Calendar doesn't exist\r\n"
#define MSG4224NOADBK "4224 Address Book doesn't exist\r\n"
#define MSG4224CANTREADMBOX "4224 Can't read mailbox\r\n"
#define MSG4224CANTREADADBK "4224 Can't read address book\r\n"
#define MSG4224CANTREADSQL "4224 Can't read sql\r\n"
#define MSG4224CANTREADCAL "4224 Can't read calendar\r\n"
#define MSG4224CANTREADADBK "4224 Can't read address book\r\n"
#define MSG4224NOUSER "4224 User does not exist\r\n"
#define MSG4225NOCALENDAR "4225 No calendar selected. CSOPEN first!\r\n"
#define MSG4225NOMBOX "4225 No mailbox selected. MBOX first!\r\n"
#define MSG4225NOADBK "4225 No address book selected. ABOOK first!\r\n"
#define MSG4226MBOXEXISTS "4226 Mailbox already exists\r\n"
#define MSG4226CALEXISTS "4226 Calendar already exists\r\n"
#define MSG4226ADBKEXISTS "4226 Calendar already exists\r\n"
#define MSG4226QUEUEOPEN "4226 Queue entry still open\r\n"
#define MSG4227TGTMBOXSELECTED "4227 Target mailbox currently selected\r\n"
#define MSG4227TGTCALSELECTED "4227 Target calendar currently selected\r\n"
#define MSG4227TGTADBKSELECTED "4227 Target address book currently selected\r\n"
#define MSG4228CANTWRITEMBOX "4228 Can't write to mailbox\r\n"
#define MSG4228CANTWRITECAL "4228 Can't write to calendar\r\n"
#define MSG4228CANTWRITEADBK "4228 Can't write to calendar\r\n"
#define MSG4229CANTWRITEMBOX "4229 Can't write to message store\r\n"
#define MSG4229CANTWRITEADBK "4229 Can't write to contact store\r\n"
#define MSG4230PATHTOOLONG "4230 Path exceeds allowed length\r\n"
#define MSG4240NOPERMISSION "4240 Permission denied\r\n"
#define MSG4241AUTHDONE "4241 Already authenticated\r\n"
#define MSG4242AUTHREQUIRED "4242 NMAP <%s>\r\n"
#define MSG4242AUTHREQUIREDF "4242 NMAP <%x%s%x>\r\n"
#define MSG4260NOQENTRY "4260 No queue entry open, try QCREA\r\n"
#define MSG4261NODOMAIN "4261 No queue entry with that domain\r\n"
#define MSG4262NOTFOUND "4262 Field/Content not found\r\n"
#define MSG4242NOTALLOWED "4242 Not allowed via FLAG\r\n"
#define MSG4243READONLY "4243 Read-only message store\r\n"
#define MSG4244NOTSUPPORTED "4244 Not supported\r\n"
#define MSG5244USERLOOKUPFAILURE "5244 Failed looking up User %s\r\n"
#define MSG5000ERROR "5000 Error requested\r\n"
#define MSG5001NOMEMORY "5001 Out of memory\r\n"
#define MSG5002SHUTDOWN "5002 Server shutting down\r\n"
#define MSG5003OVERFLOW "5003 Input too long, shutting down\r\n"
#define MSG5004NOCONNECTIONS "5004 Too many connections; try later\r\n"
#define MSG5005SQLLIBERR "5005 SQL library error\r\n"
#define MSG5006CORRUPTACL "5006 Internal error: corrupt ACL\r\n"
#define MSG5201FILEREADERR "5201 Error Reading File\r\n"
#define MSG5220QUOTAERR "5220 User Quota exceeded\r\n"
#define MSG5230NOMEMORYERR "5230 Out of memory\r\n"
#define MSG5004INTERNALERR "5004 Internal error\r\n"
#define MSG5221SPACELOW "5221 Free disk space too low\r\n"

#define DEFAULT_QUOTA_MESSAGE "  \tThe user has exceeded the allowable disk quota.\r\n"

#endif
