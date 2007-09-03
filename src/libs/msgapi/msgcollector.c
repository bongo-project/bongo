/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2006 Novell, Inc. All Rights Reserved.
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

#include <config.h>

#include <xpl.h>
#include <memmgr.h>
#include <logger.h>
#include <bongoutil.h>
#include <bongoagent.h>
#include <nmap.h>
#include <bongostore.h>
#include <msgapi.h>
#include <connio.h>
#include <bongojson.h>
#include <bongocal.h>
#include <nmlib.h>

#include <libical/ical.h>

#include <curl/curl.h>
#include "msghttp.h"

static size_t write_data(void *buffer, size_t size, size_t nmemb, void *stream);
BongoList *GetSubscribedCals(Connection *conn);

typedef struct _SubscribedCal {
    int64_t guid;
    char *name;
    char *color;
    char *url;
    char *urlUsername;
    char *urlPassword;
    long lastImportTime;
} SubscribedCal;

static SubscribedCal *
SubscribedCalNew(int64_t guid, 
                 const char *name, 
                 const char *color,
                 const char *url,
                 const char *urlUsername,
                 const char *urlPassword,
                 long lastImportTime)
{
    SubscribedCal *cal = MemMalloc(sizeof(SubscribedCal));
    cal->guid = guid;
    cal->name = MemStrdup(name);
    cal->color = color ? MemStrdup(color) : NULL;
    cal->url = MemStrdup(url);
    cal->urlUsername = urlUsername ? MemStrdup(urlUsername) : NULL;
    cal->urlPassword = urlPassword ? MemStrdup(urlPassword) : NULL;
    
    cal->lastImportTime = lastImportTime;
    return cal;
}

static void
SubscribedCalFree(SubscribedCal *cal)
{
    MemFree(cal->name);
    MemFree(cal->url);
    MemFree(cal->urlUsername);
    MemFree(cal->urlPassword);
    MemFree(cal->color);
    MemFree(cal);
}

static void
BongoSubscribedCalListFree(BongoList *list)
{
    BongoList *cur = list;
    while (cur) {
        SubscribedCalFree(cur->data);
        cur = cur->next;
    }
    BongoListFree(list);
}

static char *
ReadStream(char *s, size_t size, void *d)
{
    char *c = fgets(s, size, (FILE*)d);

    return c;
}

static icalcomponent *
ParseIcs(FILE *fh)
{
    icalparser *parser;
    icalcomponent *component;

    parser = icalparser_new();
    
    icalparser_set_gen_data(parser, fh);
    
    component = icalparser_parse(parser, ReadStream);

    if (component) {
        icalcomponent_strip_errors(component);
    }

    icalparser_free(parser);

    return component;
}

static Connection *
ConnectToStore(const char *user)
{
    char buffer[CONN_BUFSIZE + 1];
    struct sockaddr_in addr;
    Connection *conn;
    int ret;

    if ((conn = NMAPConnect("127.0.0.1", NULL)) == NULL) {
        printf("bongocollector: couldn't connect to nmap\n");
        return NULL;
    }
    
    if (!NMAPAuthenticateToStore(conn, buffer, sizeof(buffer))) {
        printf("bongocollector: couldn't authenticate\n");
        ConnClose(conn, TRUE);
        return NULL;
    }
    
    ret = NMAPRunCommandF(conn, buffer, sizeof(buffer), "USER %s\r\n", user);
    if (ret != 1000) {
        printf("bongocollector: couldn't select user '%s'\n", user);
        ConnClose(conn, TRUE);
        return NULL;
    }

    ret = NMAPRunCommandF(conn, buffer, sizeof(buffer), "STORE %s\r\n", user);
    if (ret != 1000) {
        printf("bongocollector: couldn't change store '%s'\n", user);
        ConnClose(conn, TRUE);
        return NULL;
    }

    return conn;
}

static int
ImportObject(Connection *conn, BongoJsonObject *obj, const char *calendar, const char *guid)
{
    char *doc;
    char buffer[CONN_BUFSIZE + 1];
    int len;
    
    doc = BongoJsonObjectToString(obj);
    len = strlen(doc);

    if (guid) {
        NMAPSendCommandF(conn, "REPLACE %s %d \"L/calendars/%s\"\r\n", guid, len, calendar);
    } else {
        NMAPSendCommandF(conn, "WRITE /events 3 %d \"L/calendars/%s\"\r\n", len, calendar);
    }

    if (NMAPReadAnswer(conn, buffer, sizeof(buffer), TRUE) == 2002) {
        ConnWrite(conn, doc, len);
        ConnFlush(conn);
        
        if (NMAPReadAnswer(conn, buffer, sizeof(buffer), TRUE) == 1000) {
            MemFree(doc);
            return MSG_COLLECT_OK;
        }
    }
    MemFree(doc);
    
    return MSG_COLLECT_ERROR_STORE;
}

typedef struct {
    char *guid;
    char *dtstamp;
} EventData;

static void
FreeEventData (void *data)
{
    if (data) {
        MemFreeDirect (((EventData *)data)->guid);
        MemFreeDirect (((EventData *)data)->dtstamp);
        MemFreeDirect (data);
    }
}

static BongoHashtable * 
ReadCalendar(Connection *conn, const char *calendar)
{
    int ccode;
    char buffer[CONN_BUFSIZE + 1];
    BongoHashtable *guids;
    BongoArray oldDuplicates;
    unsigned int i;
    char *guid;

    BongoArrayInit(&oldDuplicates, sizeof (char *), 8);

    guids = BongoHashtableCreateFull(16, 
                                    (HashFunction)BongoStringHash, 
                                    (CompareFunction)strcmp,
                                    MemFreeDirect,
                                    FreeEventData);
    
    ccode = NMAPRunCommandF(conn, buffer, CONN_BUFSIZE, "EVENTS \"C/calendars/%s\" Pnmap.event.uid,nmap.event.stamp\r\n", calendar);
    while (ccode == 2001) {
        char *p;
        
        p = strchr(buffer, ' ');
        if (p) {
            char *uid = NULL;
            char *dtstamp = NULL;

            *p = '\0';
            guid = MemStrdup(buffer);

            if (NMAPReadTextPropertyResponse(conn, "nmap.event.uid", buffer, CONN_BUFSIZE) == 2001) {
                EventData *oldData;
                oldData = BongoHashtableGet(guids, buffer);
                if (oldData) {
                    /*fprintf (stderr, "Found duplicate of ical uid %s in guid %s\n", buffer, guid);*/
                    uid = MemStrdup(guid);
                    BongoArrayAppendValue(&oldDuplicates, uid);
                    uid = NULL;
                } else {
                    uid = MemStrdup(buffer);
                }

            }
            if (NMAPReadTextPropertyResponse(conn, "nmap.event.stamp", buffer, CONN_BUFSIZE) == 2001 &&
                uid != NULL) {
                dtstamp = MemStrdup(buffer);
            }

            if (uid) {
                EventData *data;

                data = MemNew(EventData, 1);
                data->guid = guid;
                data->dtstamp = dtstamp;

                BongoHashtablePut(guids, uid, data);
            } else {
                MemFreeDirect(guid);
            }
        }

        ccode = NMAPReadAnswer(conn, buffer, CONN_BUFSIZE, TRUE);
    }

    for (i = 0; i < BongoArrayCount(&oldDuplicates); i++) {
        guid = BongoArrayIndex (&oldDuplicates, char *, i);

        if (ccode == 1000) {
            NMAPSendCommandF(conn, "UNLINK /calendars/%s %s\r\n", calendar, guid);
            NMAPReadAnswer(conn, buffer, sizeof(buffer), TRUE);
        }

        MemFreeDirect(guid);
    }
    BongoArrayDestroy(&oldDuplicates, TRUE);

    if (ccode == 1000) {            
        return guids;
    } else {
        BongoHashtableDelete(guids);
        return NULL;
    }
}

typedef struct {
    Connection *conn;
    const char *calendarName;
    char *buffer;
    int bufferLen;
} RemoveGuidForeachData;


static void
RemoveGuidForeach (void *key, void *value, void *data)
{
    RemoveGuidForeachData *rgfd = data;
    EventData *evtData = value;

    int ccode;

    NMAPSendCommandF(rgfd->conn, "UNLINK /calendars/%s %s\r\n", rgfd->calendarName, evtData->guid);
    ccode = NMAPReadAnswer(rgfd->conn, rgfd->buffer, rgfd->bufferLen, TRUE);
}

static int
ImportJson(BongoArray *objects, 
           const char *user, 
           const char *calendarName,
           const char *icsName,
           Connection *existingConn)
{
    char buffer[CONN_BUFSIZE + 1];
    Connection *conn;
    int ret;
    unsigned int i;
    BongoHashtable *guids;

    if (existingConn) {
        conn = existingConn;
    } else {
        conn = ConnectToStore(user);
        if (!conn) {
            return MSG_COLLECT_ERROR_STORE;
        }
    }

    if (icsName) {
        snprintf(buffer, sizeof(buffer), "/calendars/%s", calendarName);
        if (NMAPSetTextPropertyFilename(conn, buffer, "bongo.calendar.icsname", icsName, strlen(icsName)) < 0) {
            ret = MSG_COLLECT_ERROR_STORE;
            goto done;
        }
    }

    guids = ReadCalendar(conn, calendarName);

    ret = MSG_COLLECT_OK;

    for (i = 0; i < objects->len; i++) {
        BongoJsonObject *event;
        if (BongoJsonArrayGetObject(objects, i, &event) == BONGO_JSON_OK) {
            BongoCalObject *cal;
            EventData *evtData = NULL;
            int res = MSG_COLLECT_OK;
            char *guid = NULL;
            BOOL import = TRUE;

            cal = BongoCalObjectNew(event);
            assert (cal != NULL);
            if (guids) {
                const char *uid;
                const char *stamp;

                uid = BongoCalObjectGetUid(cal);

                if (uid) {
                    evtData = BongoHashtableRemoveFull(guids, uid, TRUE, FALSE);
                }
                if (evtData) {
                    guid = evtData->guid;
                    if (evtData->dtstamp) {
                        stamp = BongoCalObjectGetStamp(cal);
                        if (stamp && !strcmp (stamp, evtData->dtstamp)) {
                            /* fprintf (stderr, "Skipping guid: %s uid: %s stamp: %s\n",
                               guid, uid, stamp); */
                            import = FALSE;
                        }
                    }
                }
            }
                
            if (import) {
                res = ImportObject(conn, event, calendarName, guid);
            }
            FreeEventData (evtData);

            if (res != MSG_COLLECT_OK) {
                ret = res;
            }

            BongoCalObjectFree(cal, FALSE);
        }
    }

    /* remove guids that are no longer in the ics file */
    if (guids) {
        RemoveGuidForeachData rtfd;
            
        rtfd.conn = conn;
        rtfd.calendarName = calendarName;
        rtfd.buffer = buffer;
        rtfd.bufferLen = sizeof (buffer);

        BongoHashtableForeach(guids, RemoveGuidForeach, &rtfd);            
        BongoHashtableDelete(guids);
    }

done:

    if (!existingConn) {
        ConnClose(conn, TRUE);
        ConnFree(conn);
    }
    
    return ret;
}

int
MsgImportIcs(FILE *fh, 
             const char *user, 
             const char *calendarName, 
             const char *calendarColor,
             Connection *conn)
{
    icalcomponent *component;
    BongoJsonObject *obj;
    BongoCalObject *cal;
    BongoArray *objects;
    int ret;
    const char *icsName;
    char *icsNameDup = NULL;
    
    component = ParseIcs(fh);
    if (!component) {
        return MSG_COLLECT_ERROR_PARSE;
    }
    
    obj = BongoCalIcalToJson(component);
    icalcomponent_free(component);

    if (!obj) {
        return MSG_COLLECT_ERROR_PARSE;
    }

    cal = BongoCalObjectNew(obj);
    
    icsName = (char *)BongoCalObjectGetCalendarName(cal);

    if (icsName) {
        icsNameDup = MemStrdup(icsName);
    }

    BongoCalObjectStripSystemTimezones(cal);
    BongoCalObjectFree(cal, FALSE);

    objects = BongoCalSplit(obj);
    if (!objects) {
        return MSG_COLLECT_ERROR_PARSE;
    }

    ret = ImportJson(objects, user, calendarName, icsNameDup, conn);

    if (icsNameDup) {
        MemFree(icsNameDup);
    }

    BongoJsonArrayFree(objects);

    return ret;
}

BongoList *
GetSubscribedCals(Connection *conn)
{
    char infoBuffer[CONN_BUFSIZE + 1];
    char urlBuffer[CONN_BUFSIZE + 1];
    char urlUsernameBuffer[CONN_BUFSIZE + 1];
    char urlPasswordBuffer[CONN_BUFSIZE + 1];
    char ownerBuffer[CONN_BUFSIZE + 1];
    char *info[7];
    NMAPSendCommandF(conn, "LIST /calendars Pbongo.calendar.url,bongo.calendar.username,bongo.calendar.password,bongo.calendar.owner,bongo.calendar.date\r\n");
    BongoList *ret = NULL;

    while (NMAPReadAnswer(conn, infoBuffer, sizeof(infoBuffer), TRUE) == 2001) {
        uint64_t guid;
        char *endp;
        long date;

        int urlccode;
        int urlusernameccode;
        int urlpasswordccode;
        int ownerccode;
        int dateccode;

        urlccode = NMAPReadTextPropertyResponse(conn, "bongo.calendar.url",
                                                urlBuffer, sizeof(urlBuffer));
        urlusernameccode = NMAPReadTextPropertyResponse(conn, "bongo.calendar.username",
                                                urlUsernameBuffer, sizeof(urlUsernameBuffer));
        urlpasswordccode = NMAPReadTextPropertyResponse(conn, "bongo.calendar.password",
                                                        urlPasswordBuffer, sizeof(urlPasswordBuffer));
        ownerccode = NMAPReadTextPropertyResponse(conn, "bongo.calendar.owner", 
                                                  ownerBuffer, sizeof(ownerBuffer));
        dateccode = NMAPReadDecimalPropertyResponse(conn, "bongo.calendar.date",
                                                    &date);
        if (urlccode == 3245) {
            /* The current calendar is not a subscription */
            continue;
        }
        if (ownerccode != 3245) {
            /* The current calendar is a local subscription, it won't be collected */
            continue;
        }
        
        if (dateccode == 3245) {
            date = 0;
        }

        if (urlccode != 2001) {
            continue;
        }

        CommandSplit(infoBuffer, info, 7);

        guid = HexToUInt64(info[0], &endp);

        SubscribedCal *cal = SubscribedCalNew(guid, info[6], NULL, urlBuffer, 
                                              urlusernameccode == 2001 ? urlUsernameBuffer : NULL,
                                              urlpasswordccode == 2001 ? urlPasswordBuffer : NULL,
                                              date);
        ret = BongoListPrepend(ret, cal);
    }

    return ret;
}

static int
MsgImportSubscribedCal(const char *user, SubscribedCal *cal, Connection *conn, uint64_t *guidOut)
{
    char buffer[CONN_BUFSIZE + 1];
    int ccode;
    char *info[7];
    char *endp;
    uint64_t guid = 0;

    ccode = NMAPRunCommandF(conn, buffer, sizeof(buffer),
                            "INFO \"/calendars/%s\"\r\n", cal->name);

    if (ccode == 4224) {
        /* create calendar */
        ccode = NMAPRunCommandF(conn, buffer, sizeof(buffer),
                                "WRITE /calendars %d 0 \"F%s\"\r\n",
                                STORE_DOCTYPE_CALENDAR, cal->name);

        ccode = NMAPReadAnswer(conn, buffer, sizeof(buffer), TRUE);
        if (ccode == 1000) {
            CommandSplit(buffer, info, 2);
            guid = HexToUInt64(info[0], &endp);
            NMAPSetTextProperty(conn, guid, "bongo.calendar.url",
                                cal->url, strlen(cal->url));
            NMAPSetTextProperty(conn, guid, "bongo.calendar.type",
                                "web", strlen("web"));
            if (cal->urlUsername) {
                NMAPSetTextProperty(conn, guid, "bongo.calendar.urlusername",
                                    cal->urlUsername, strlen(cal->urlUsername));
            }

            if (cal->urlPassword) {
                NMAPSetTextProperty(conn, guid, "bongo.calendar.urlpassword",
                                    cal->urlPassword, strlen(cal->urlPassword));
            }

            if (cal->color) {
                NMAPSetTextProperty(conn, guid, "bongo.calendar.color",
                                    cal->color, strlen(cal->color));
            }
        }
    } else if (ccode == 2001) {
        CommandSplit(buffer, info, 7);
        guid = HexToUInt64(info[0], &endp);

        /* eat the 1000 OK line */
        NMAPReadAnswer(conn, buffer, sizeof(buffer), TRUE);
    }
    
    ccode = MsgImportIcsUrl(user, cal->name, cal->color, cal->url, cal->urlUsername, cal->urlPassword, conn);
    
    *guidOut = guid;    
    return ccode;
}

static int
MsgImportCal(const char *user, SubscribedCal *cal, Connection *conn, uint64_t *guidOut)
{
    char buffer[CONN_BUFSIZE + 1];
    int ccode;
    char *info[7];
    char *endp;
    uint64_t guid = 0;

    ccode = NMAPRunCommandF(conn, buffer, sizeof(buffer),
                            "INFO \"/calendars/%s\"\r\n", cal->name);

    if (ccode == 4224) {
        /* create calendar */
        ccode = NMAPRunCommandF(conn, buffer, sizeof(buffer),
                                "WRITE /calendars %d 0 \"F%s\"\r\n",
                                STORE_DOCTYPE_CALENDAR, cal->name);
        ccode = NMAPReadAnswer(conn, buffer, sizeof(buffer), TRUE);
        if (ccode == 1000 && cal->color) {
            CommandSplit(buffer, info, 2);
            guid = HexToUInt64(info[0], &endp);
            NMAPSetTextProperty(conn, guid, "bongo.calendar.color",
                                cal->color, strlen(cal->color));
        }
        
    } else if (ccode == 2001) {
        CommandSplit(buffer, info, 7);
        guid = HexToUInt64(info[0], &endp);

        /* eat the 1000 OK line */
        NMAPReadAnswer(conn, buffer, sizeof(buffer), TRUE);
    }

    ccode = MsgImportIcsUrl(user, cal->name, cal->color, cal->url, cal->urlUsername, cal->urlPassword, conn);

    *guidOut = guid;
    return ccode;
}

int
MsgCollectUser(const char *user)
{
    Connection *conn = ConnectToStore(user);
    BongoList *cals;
    BongoList *cur;

    if (!conn) {
        return MSG_COLLECT_ERROR_STORE;
    }

    cals = GetSubscribedCals(conn);
    cur = cals;

    while (cur) {
        uint64_t guid;
        MsgImportSubscribedCal(user, (SubscribedCal*)cur->data, conn, &guid);
        cur = cur->next;
    }

    ConnClose(conn, TRUE);
    ConnFree(conn);

    BongoSubscribedCalListFree(cals);
    return MSG_COLLECT_OK;
}

void
MsgCollectAllUsers(void)
{
    BongoArray *users;
    int i;

    if (!MsgAuthUserList(&users))
        return;

    for(i = 0; i < BongoArrayCount(users); i++) {
        char *user = BongoArrayIndex(users, char *, i);
        MsgCollectUser(user);
    }

    BongoArrayFree(users, TRUE);
}

size_t
write_data(void *buffer, size_t size, size_t nmemb, void *stream)
{
    fwrite(buffer, size, nmemb, stream);
    return nmemb*size;
}

static char *
AliasProtocol(const char *url, const char *proto, const char *newProto)
{
    unsigned int protoLen = strlen(proto);

    if (strlen(url) > protoLen && XplStrNCaseCmp(proto, url, protoLen) == 0) {
        return MemStrCat(newProto, url + protoLen);
    }

    return NULL;
}

int
MsgImportIcsUrl(const char *user,
                const char *calendarName, 
                const char *calendarColor, 
                const char *url,
                const char *urlUsername,
                const char *urlPassword,
                Connection *existingConn)
{
    CURL *curl;
    CURLcode res;
    char filename[XPL_MAX_PATH];
    char *aliasedUrl = NULL;
    int ret = 0;

    /* replace webcal: with http: */
    aliasedUrl = AliasProtocol(url, "webcal:", "http:");
    if (!aliasedUrl) {
        aliasedUrl = AliasProtocol(url, "feed:", "http:");
    }

    curl = curl_easy_init();
    if (curl) {
        int ccode;
        FILE *fh = XplOpenTemp("/tmp", "w+", filename);
        char cred[1024];

        curl_easy_setopt(curl, CURLOPT_FOLLOWLOCATION, TRUE);
        if (aliasedUrl) {
            curl_easy_setopt(curl, CURLOPT_URL, aliasedUrl);
        } else {
            curl_easy_setopt(curl, CURLOPT_URL, url);
        }
#if 0
        curl_easy_setopt(curl, CURLOPT_VERBOSE, TRUE);
#endif
        curl_easy_setopt(curl, CURLOPT_WRITEFUNCTION, *write_data);
        curl_easy_setopt(curl, CURLOPT_WRITEDATA, fh);

        if (urlUsername && urlPassword) {
            snprintf(cred, 1024, "%s:%s", urlUsername, urlPassword);
            curl_easy_setopt(curl, CURLOPT_USERPWD, cred);
        }

        res = msg_curl_easy_perform_cached(curl);

        curl_easy_cleanup(curl);

        if (res == CURLE_OK) {
            /* import the ics */
            fseek(fh, 0, SEEK_SET);
            ccode = MsgImportIcs(fh, user, calendarName, calendarColor, existingConn);
        } else {
            switch (res) {
            case CURLE_UNSUPPORTED_PROTOCOL :
            case CURLE_URL_MALFORMAT :
                ccode = MSG_COLLECT_ERROR_URL;
                break;
            case CURLE_FTP_ACCESS_DENIED :
            case CURLE_BAD_PASSWORD_ENTERED :
#if LIBCURL_VERSION_NUM >= 0x070f02
// our import curl is the above version. if it's older, it might not have
// this error defined. Needed for Centos 4.4 (curl 7.12)
            case CURLE_LOGIN_DENIED :
#endif
                ccode = MSG_COLLECT_ERROR_AUTH;
                break;
            default :
                ccode = MSG_COLLECT_ERROR_DOWNLOAD;
                break;
            }
        }

        fclose(fh);
        unlink(filename);

        ret = ccode;
    }

    if (aliasedUrl) {
        MemFree(aliasedUrl);
    }

    return ret;
}

int
MsgIcsSubscribe(const char *user, const char *name, const char *color, const char *url, const char *urlUsername, const char *urlPassword, uint64_t *guidOut)
{
    Connection *conn = ConnectToStore(user);
    SubscribedCal *cal;
    int ret;

    if (!conn) {
        return MSG_COLLECT_ERROR_STORE;
    }

    cal = SubscribedCalNew(0, name, color, url, urlUsername, urlPassword, 0);
    ret = MsgImportSubscribedCal(user, cal, conn, guidOut);

    ConnClose(conn, TRUE);
    ConnFree(conn);

    SubscribedCalFree(cal);
    
    return ret;
}

int
MsgIcsImport(const char *user, const char *name, const char *color, const char *url, const char *urlUsername, const char *urlPassword, uint64_t *guidOut)
{
    Connection *conn = ConnectToStore(user);
    SubscribedCal *cal;
    int ret;

    if (!conn) {
        return MSG_COLLECT_ERROR_STORE;
    }

    cal = SubscribedCalNew(0, name, color, url, urlUsername, urlPassword, 0);
    ret = MsgImportCal(user, cal, conn, guidOut);

    ConnClose(conn, TRUE);
    ConnFree(conn);

    SubscribedCalFree(cal);

    return ret;
}
