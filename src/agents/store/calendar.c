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
#include <bongoutil.h>
#include <assert.h>
#include <limits.h>
#include <time.h>

#include "stored.h"
#include "messages.h"
#include "object-model.h"
#include "calendar.h"


// this is called on any event which is saved to the store
const char *
StoreProcessIncomingEvent(StoreClient *client,
                          StoreObject *event,
                          uint64_t linkGuid)
{
    BongoJsonNode *node = NULL;
    BongoCalObject *cal = NULL;
    BongoCalTime start;
    BongoCalTime end;

	if (linkGuid) {
		// FIXME: Link this event into a calendar
	}

    /* FIXME */

    if (GetJson(client, event, &node, NULL) != BONGO_JSON_OK)
        goto parse_error;

    if (node->type != BONGO_JSON_OBJECT)
        goto parse_error;
    
    cal = BongoCalObjectNew(BongoJsonNodeAsObject(node));

    /* Don't need the node anymore */
    BongoJsonNodeFreeSteal(node);

    start = BongoCalObjectGetStart(cal);
    end = BongoCalObjectGetEnd(cal);

    /* Force to real times */
    start.isDate = FALSE;
    end.isDate = FALSE;

    /*
     * FIXME: need to get these event-specific properties from the object
     *
    BongoCalTimeToIcal(start, info->data.event.start, sizeof(info->data.event.start));
    BongoCalTimeToIcal(end, info->data.event.end, sizeof(info->data.event.end));    
    
    info->data.event.summary = BongoCalObjectGetSummary(cal);
    info->data.event.location = BongoCalObjectGetLocation(cal);
    info->data.event.uid = BongoCalObjectGetUid(cal);
    info->data.event.stamp = BongoCalObjectGetStamp(cal);

    if (DStoreSetEvent(client->handle, info) ||
        DStoreSetDocInfo(client->handle, info)) 
    {
        BongoCalObjectFree(cal, TRUE);
        return MSG5005DBLIBERR;
    }
	*/
	
	StoreObjectSave(client, event); // FIXME: is this necessary?
	BongoCalObjectFree(cal, TRUE);
    return NULL;

parse_error:
    if (node) {
        BongoJsonNodeFree(node);
    }
    if (cal) {
        BongoCalObjectFree(cal, TRUE);
    }

    return MSG4226BADEVENT;
}

#if 0
static int
ParseAlarmJson(BongoJsonObject *json, BongoArray *result, BongoMemStack *memstack)
{
    BongoArray *alarms;
    unsigned int i;
    AlarmInfo *alarm = NULL;

    if (BongoJsonObjectGetArray(json, "alarms", &alarms)) {
        return -1;
    }

    for (i = 0; i < alarms->len; i++) {
        BongoJsonObject *object;
        const char *text;
        char *endptr;

        alarm = MemMalloc(sizeof(AlarmInfo));
        memset(alarm, 0, sizeof(AlarmInfo));

        if (BongoJsonArrayGetObject(alarms, i, &object)) {
            goto abort;
        }
        
        if (BongoJsonObjectGetString(object, "summary", &text)) {
            goto abort;
        }
        alarm->summary = BongoMemStackPushStr(memstack, text);

        if (BongoJsonObjectGetString(object, "when", &text)) {
            goto abort;
        }
        alarm->offset = strtoul(text, &endptr, 10); 
        if (*endptr) {
            goto abort;
        }

        if (!BongoJsonObjectGetString(object, "email", &text)) {
            alarm->email = BongoMemStackPushStr(memstack, text);
        }

        if (!BongoJsonObjectGetString(object, "sms", &text)) {
            alarm->sms = BongoMemStackPushStr(memstack, text);
        }

        BongoArrayAppendValue(result, alarm);
    }

    return 0;

abort:
    printf("couldn't parse alarm json\n");

    if (alarm) {
        MemFree(alarm);
    }
    return -1;
}
#endif

/* FIXME: this function probably leaks a lot of memory */

// this is called when an alarm property is set on an event.
CCode
StoreSetAlarm(StoreClient *client, 
              StoreObject *event, // docinfo
              const char *alarmtext)
{
	if (client) {} // compiler error FIXME
	if (event) {} // compiler error FIXME
	if (alarmtext) {} // compiler error FIXME

	return 0;
#if 0
	// FIXME awful awful.
    CCode ccode = 0;
    BongoJsonNode *node = NULL;
    BongoCalObject *cal = NULL;
    BongoJsonNode *alarmjson = NULL;
    BongoCalTime start;
    BongoCalTime end;
    BongoArray alarms;
    BongoArray *occurrences = NULL;
    unsigned int i;
    unsigned int j;

    /* parse the alarm info */

    if (BONGO_JSON_OK != BongoJsonParseString(alarmtext, &alarmjson)) {
        return ConnWriteStr(client->conn, MSG3026BADALARM);
    }

    if (BongoArrayInit(&alarms, sizeof(AlarmInfo *), 4)) {
        return ConnWriteStr(client->conn, MSG5001NOMEMORY);
    }

    if (ParseAlarmJson(BongoJsonNodeAsObject(alarmjson), &alarms, &client->memstack)) {
        ccode = ConnWriteStr(client->conn, MSG3026BADALARM);
        goto finish;
    }

    /* load the event, and compute occurrences */

    if (BONGO_JSON_OK != GetJson(client, event, &node, NULL) ||
        BONGO_JSON_OBJECT != node->type) 
    {
        ccode = ConnWriteStr(client->conn, MSG4226BADEVENT);
        goto finish;
    }

    cal = BongoCalObjectNew(BongoJsonNodeAsObject(node));
    BongoJsonNodeFreeSteal(node);

    start = BongoCalTimeNow(NULL);
    end = BongoCalTimeAdjust(start, 365, 0, 0, 0); /* generate instances for 1 year */

    occurrences = BongoArrayNew(sizeof(BongoCalOccurrence), 8);
    BongoCalObjectCollect(cal, start, end, NULL, TRUE, occurrences);

    /* delete any old alarms with this guid */
    AlarmDBDeleteAlarms(client->system.alarmdb, client->storeName, event->guid);

    /* for each instance, add alarms */
    for (i = 0; i < occurrences->len; i++) {
        BongoCalOccurrence occ = BongoArrayIndex(occurrences, BongoCalOccurrence, i);

        for (j = 0; j < alarms.len; j++) {
            AlarmInfo *alarm = BongoArrayIndex(&alarms, AlarmInfo *, j);
            
            if (!alarm) {
                ccode = ConnWriteStr(client->conn, MSG5004INTERNALERR);
                goto finish;
            }

            alarm->guid = event->guid;
            alarm->store = client->storeName;

            if (!alarm->summary) {
                alarm->summary = BongoCalInstanceGetSummary(occ.instance);
            }

            alarm->time = BongoCalInstanceGetStart(occ.instance);
            alarm->time = BongoCalTimeAdjust(alarm->time, 0, 0, alarm->offset, 0);

            if (AlarmDBSetAlarm(client->system.alarmdb, alarm)) {
                ccode = ConnWriteStr(client->conn, MSG5005DBLIBERR);
                goto finish;
            }
        }
    }

    /* it worked */
    ccode = 0;

finish:

    for (i = 0; i < alarms.len; i++) {
        MemFree(BongoArrayIndex(&alarms, AlarmInfo *, i));
    }
    BongoArrayDestroy(&alarms, TRUE);

    if (node) {
        BongoJsonNodeFree(node);
    }
    if (cal) {
        BongoCalObjectFree(cal, TRUE);
    }

    return ccode;
#endif
}
