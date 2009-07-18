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

#ifndef BONGO_CAL_PRIVATE_H
#define BONGO_CAL_PRIVATE_H

#include <bongojson.h>
#include <bongocal.h>

/* This is the maximum year we will go up to (inclusive). Since we use time_t
   values we can't go past 2037 anyway, and some of our VTIMEZONEs may stop
   at 2037 as well. */
/* FIXME: that isn't actually true */
#define MAX_YEAR        2999


struct _BongoCalObject {
    BongoJsonObject *json;

    GArray *instances;
    BOOL instancesSorted;
    BongoHashtable *timezones;
    
    BOOL methodRead;
    BongoCalMethod method;

    const char *calName;
};

struct _BongoCalInstance {
    BongoCalObject *cal;
    BongoJsonObject *json;

    BongoCalTime start;
    BongoCalTime end;
    BongoCalDuration duration;
    
    const char *uid;
    const char *stamp;
    BongoCalTime recurid;
    const char *tzid;

    const char *summary;
    const char *location;
    const char *description;

    BOOL hasTzOffsetFrom;
    int tzOffsetFrom;
    
    BOOL hasTzOffsetTo;
    int tzOffsetTo;

    BongoSList *rdates;
    BongoSList *rrules;
    BongoSList *exdates;
    BongoSList *exrules;
};

typedef enum {
    BONGO_CAL_TIMEZONE_OFFSET,
    BONGO_CAL_TIMEZONE_SYSTEM,
    BONGO_CAL_TIMEZONE_JSON
} BongoCalTimezoneType;

struct _BongoCalTimezone {
    BongoCalTimezoneType type;
    char *tzid;

    /* Offset timezones */
    int offset;

    /* System and json-backed timezones */
    GArray *changes;

    /* Json-backed timezones */
    BongoCalObject *cal;
    BongoJsonObject *json;
    uint64_t changesStart;
    uint64_t changesEnd;
};

typedef BOOL (* BongoCalRecurInstanceFn) (BongoCalInstance *inst,
                                         uint64_t instanceStart,
                                         uint64_t instanceEnd,
                                         void *data);

typedef BongoCalTimezone* (* BongoCalRecurResolveTimezoneFn) (const char *tzid,
							    void *data);

void BongoCalRecurGenerateInstances (BongoCalInstance *inst,
				    uint64_t start,
				    uint64_t end,
				    BongoCalRecurInstanceFn cb,
				    void *cbData,
				    BongoCalRecurResolveTimezoneFn tzCb,
				    void *tzCbData,
				    BongoCalTimezone *defaultTimezone);

BOOL BongoCalRecurEnsureEndDates (BongoCalInstance *inst,
                                 BOOL refresh,
                                 BongoCalRecurResolveTimezoneFn tzCb,
                                 void *tzCbData);


BOOL BongoCalTimezoneInit(const char *cachedir);

#endif
