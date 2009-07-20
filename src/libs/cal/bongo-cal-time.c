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

/* Much of this code comes from libical: */

/*
 * (C) COPYRIGHT 2000, Eric Busboom, http://www.softwarestudio.org
 *
 * This program is free software; you can redistribute it and/or modify
 * it under the terms of either:
 *
 *    The LGPL as published by the Free Software Foundation, version
 *    2.1, available at: http://www.fsf.org/copyleft/lesser.html
 *
 *  Or:
 *
 *  The Mozilla Public License Version 1.0. You may obtain a copy of
 *  the License at http://www.mozilla.org/MPL/
 * The Original Code is eric. The Initial Developer of the Original
 * Code is Eric Busboom */


#include <config.h>
#include "bongo-cal-private.h"
#include <bongocal.h>
#include <libical/ical.h>

#define ASSERTTZ(t) {assert(!t.isUtc || (t.isUtc && t.tz == NULL) || (t.isUtc && t.tz == BongoCalTimezoneGetUtc())); assert(!t.tz || !t.tzid  || (!strcmp(t.tzid,  BongoCalTimezoneGetTzid(t.tz)))); }

static int NumLeap(int64_t y1, int64_t y2) {
  --y1;
  --y2;
  return (y2/4 - y1/4) - (y2/100 - y1/100) + (y2/400 - y1/400);
}

static const BongoCalTime emptyTime = { 0, 0, 0, 0, 0, 0, 0, TRUE, NULL, NULL};
static const BongoCalDuration emptyDuration = { 0, 0, 0, 0, 0, 0};

static const unsigned int daysInMonth[12] = {31,28,31,30,31,30,31,31,30,31,30,31};
static const unsigned int daysInYear[2][14] = {
  {  0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334, 365 },
  {  0, 31, 60, 91, 121, 152, 182, 213, 244, 274, 305, 335, 366 }
};


static char *monthShortNames[12] = { "Jan", "Feb", "Mar", "Apr", "May", "Jun",
                                     "Jul", "Aug", "Sep", "Oct", "Nov", "Dec" };

static char *weekdayShortNames[8] = { NULL, "Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun" };

BOOL
BongoCalIsLeapYear(int year)
{
    return ((((year % 4) == 0) 
             && ((year % 100) != 0)) ||
            (year % 400) == 0 );
}

int
BongoCalDaysInMonthNoLeap(int month) 
{
    BongoReturnValIfFail((month >= 0 && month < 12), 0);
    return daysInMonth[month];
}

int
BongoCalDaysInMonth(int month, int year)
{
    BongoReturnValIfFail((month >= 0 && month < 12), 0);
    
    return daysInMonth[month] + ((month == 1 && BongoCalIsLeapYear(year)) ? 1 : 0);
}

BongoCalTime 
BongoCalTimeEmpty(void) 
{
    return emptyTime;
}

BongoCalDuration
BongoCalDurationEmpty(void)
{
    return emptyDuration;
}

BongoCalTime 
BongoCalTimeNewDate(int day, int month, int year)
{
    BongoCalTime ret = emptyTime;
    
    ret.isDate = TRUE;
    ret.day = day;
    ret.month = month;
    ret.year = year;

    ASSERTTZ(ret);

    return ret;
}

BongoCalTime 
BongoCalTimeNow(BongoCalTimezone *tz)
{
    return BongoCalTimeNewFromUint64((uint64_t)time(NULL), FALSE, tz);
}

BongoCalTime
BongoCalTimeSetTimezone(BongoCalTime t, BongoCalTimezone *tz)
{
    if (tz == NULL) {
        tz = BongoCalTimezoneGetUtc();
    }
    
    if (tz == BongoCalTimezoneGetUtc()) {
        t.isUtc = TRUE;
    } else {
        t.isUtc = FALSE;
    }
    
    t.tz = tz;
    t.tzid = BongoCalTimezoneGetTzid(tz);

    return t;
}

BongoCalTime
BongoCalTimeSetTzid(BongoCalTime t, const char *tzid)
{
    if (!strcmp(tzid, "UTC")) {
        return BongoCalTimeSetTimezone(t, BongoCalTimezoneGetUtc());
    } else {
        t.isUtc = FALSE;
        t.tz = NULL;
        t.tzid = tzid;
        return t;
    }
}

static BongoCalTime
BongoCalTimeFromUint64UTC(uint64_t src)
{
    int64_t days;
    int64_t remainder;    
    int64_t year;
    BongoCalTime t = emptyTime;
    int index;

    days = src / (60 * 60 * 24);
    remainder = src % (60 * 60 * 24);

    t.hour = remainder / (60 * 60);
    remainder %= (60 * 60);
    
    t.minute = remainder / 60;
    t.second = remainder % 60;

    year = 1970;
    
    while (days < 0 || days >= (BongoCalIsLeapYear(year) ? 366 : 365)) {
        int64_t guess;
        int64_t numDays;

        guess = year + (days / 365) - (days % 365 < 0);

        numDays = ((guess - year) * 365 + NumLeap(year, guess));
        days -= numDays;
        year = guess;
    }
    
    index = BongoCalIsLeapYear(year);
    for (t.month = 11; days < daysInYear[index][t.month]; t.month--) {
        continue;
    }
    
    t.year = year;
    days -= daysInYear[index][t.month];
    t.day = days + 1;
    ASSERTTZ(t);
    
    return t;
}


BongoCalTime
BongoCalTimeNewFromUint64(uint64_t src, BOOL isDate, BongoCalTimezone *tz)
{
    BongoCalTime t;
    
    t = BongoCalTimeFromUint64UTC(src);
    t = BongoCalTimeSetTimezone(t, BongoCalTimezoneGetUtc());
    t.isDate = isDate;

    ASSERTTZ(t);
    
    if (tz) {
        t = BongoCalTimezoneConvertTime(t, tz);
    }
    ASSERTTZ(t);
    
    return t;
}

BOOL
BongoCalTimeIsEmpty(BongoCalTime t)
{
    ASSERTTZ(t);
    if (t.second + t.minute + t.hour + t.day + t.month + t.year == 0) {
        return TRUE;
    } else {
        return FALSE;
    }
}

BOOL
BongoCalDurationIsEmpty(BongoCalDuration d)
{
    if (d.weeks + d.days + d.hours + d.minutes + d.seconds == 0) {
        return TRUE;
    } else {
        return FALSE;
    }
}

int 
BongoCalTimeCompareDateOnly(BongoCalTime a, BongoCalTime b) 
{
    int ret;
    ASSERTTZ(a);
    ASSERTTZ(b);
    
    /* Convert to utc */
    a = BongoCalTimezoneConvertTime(a, NULL);
    b = BongoCalTimezoneConvertTime(b, NULL);

    ret = a.year - b.year;
    if (ret) {
        return ret;
    }

    ret = a.month - b.month;
    if (ret) {
        return ret;
    }
    
    ret = a.day - b.day;
    return ret;
}

int 
BongoCalTimeCompare(BongoCalTime a, BongoCalTime b) 
{
    int ret;

    ASSERTTZ(a);
    ASSERTTZ(b);

    /* Convert to utc */
    a = BongoCalTimezoneConvertTime(a, NULL);
    b = BongoCalTimezoneConvertTime(b, NULL);

    ret = a.year - b.year;
    if (ret) {
        return ret;
    }

    ret = a.month - b.month;
    if (ret) {
        return ret;
    }
    
    ret = a.day - b.day;
    if (ret) {
        return ret;
    }

    ret = a.hour - b.hour;
    if (ret) {
        return ret;
    }

    ret = a.minute - b.minute;
    if (ret) {
        return ret;
    }
    
    ret = a.second - b.second;
    
    return ret;
}

uint64_t
BongoCalTimeUtcAsUint64(BongoCalTime t)
{
    uint64_t year, days, hours, minutes, ret;
    int i;
    ASSERTTZ(t);

    if (BongoCalTimeIsEmpty(t)) {
        return 0;
    }

    if (t.year < 1970) {
        return 0;
    }

    year = t.year;

    assert(t.month >= 0 && t.month <= 11);

    days = 365 * (year - 1970) + NumLeap(1970, year);

    for (i = 0; i < t.month; i++) {
        days += daysInMonth[i];
    }
    
    if (t.month > 1 && BongoCalIsLeapYear(year)) {
        days++;
    }
    
    days += t.day - 1;
    hours = days * 24 + t.hour;
      
    minutes = hours * 60 + t.minute;
    ret = minutes * 60 + t.second;
    
    return ret;
}

uint64_t
BongoCalTimeAsUint64(BongoCalTime t)
{   
    if (BongoCalTimeIsEmpty(t)) {
        return 0;
    }
    ASSERTTZ(t);
    t = BongoCalTimezoneConvertTime(t, BongoCalTimezoneGetUtc());
    
    return BongoCalTimeUtcAsUint64(t);
}

/* The combined "fix up this time and convert it to uint64" semantics
 * of mktime, for porting ease */
uint64_t
BongoCalMkTime(BongoCalTime *t)
{
    *t = BongoCalTimeAdjust(*t, 0, 0, 0, 0);
    return BongoCalTimeAsUint64(*t);
}


BongoCalTime
BongoCalTimeParseIcal(const char *str)
{
    BongoCalTime t = emptyTime;
    int size;
    
    if (!str) {
        return emptyTime;
    }

    size = strlen(str);
    
    if (size == 15 || size == 19) {
        t.isUtc = FALSE;
        t.isDate = FALSE;
    } else if (size == 16 || size == 20) {
        if (str[15] != 'Z' && str[19] != 'Z') {
            goto fail;
        }
        
        t = BongoCalTimeSetTimezone(t, BongoCalTimezoneGetUtc());
        t.isDate = 0;
    } else if (size == 8 || size == 10) {
        t.isUtc = FALSE;
        t.isDate = TRUE;
    } else {
        goto fail;
    }
    
    if (t.isDate) {
        if (size == 10) {
            char dsep1, dsep2;
            if (sscanf(str,"%04d%c%02d%c%02d",&t.year,&dsep1,&t.month,&dsep2,&t.day) < 5)
                goto fail;
            if ((dsep1 != '-') || (dsep2 != '-'))
                goto fail;
        } else if (sscanf(str,"%04d%02d%02d",&t.year,&t.month,&t.day) < 3) {
            goto fail;
        }
    } else {
        if (size > 16 ) {
            char dsep1, dsep2, tsep, tsep1, tsep2;
            if (sscanf(str,"%04d%c%02d%c%02d%c%02d%c%02d%c%02d",&t.year,&dsep1,&t.month,&dsep2,
                       &t.day,&tsep,&t.hour,&tsep1,&t.minute,&tsep2,&t.second) < 11)
                goto fail;
            
            if((tsep != 'T') || (dsep1 != '-') || (dsep2 != '-') || (tsep1 != ':') || (tsep2 != ':'))
                goto fail;
            
        } else {
            char tsep;
            if (sscanf(str,"%04d%02d%02d%c%02d%02d%02d",&t.year,&t.month,&t.day,
                       &tsep,&t.hour,&t.minute,&t.second) < 7)
                goto fail;
            
            if(tsep != 'T')
                goto fail;
        }
    }
    /* BongoCalTime is zero-based, ical times are 1-based */
    t.month--;

    /* The adjust function will correct out-of-bounds dates */
    t = BongoCalTimeAdjust(t, 0, 0, 0, 0);
    
    ASSERTTZ(t);

    return t;

fail:
    return BongoCalTimeEmpty();
}

void
BongoCalTimeToIcal(BongoCalTime t, char *buffer, int buflen)
{
    ASSERTTZ(t);
    
    if (t.isDate) {
        snprintf(buffer, buflen, "%04d%02d%02d", t.year, t.month + 1, t.day);
    } else {
        snprintf(buffer, buflen, "%04d%02d%02dT%02d%02d%02d%s",
                 t.year,
                 t.month + 1,
                 t.day,
                 t.hour,
                 t.minute,
                 t.second,
                 t.isUtc ? "Z" : "");
    }
}

int 
BongoCalTimeToStringConcise(BongoCalTime t, char *buffer, int buflen)
{
    uint64_t tnow, twhen;
    BongoCalTime now;
    char tmbuf[10];

    now = BongoCalTimeNow(NULL);
    tnow = BongoCalTimeAsUint64(now);
    twhen = BongoCalTimeAsUint64(t);

    if (t.hour >= 12) {
        if (t.minute) {
            snprintf (tmbuf, sizeof(tmbuf), "%d:%02dPM", 
                      12 == t.hour ? 12 : t.hour - 12, 
                      t.minute);
        } else {
            snprintf (tmbuf, sizeof(tmbuf), "%dPM", 
                      12 == t.hour ? 12 : t.hour - 12);
        }
    } else {
        if (t.minute) {
            snprintf (tmbuf, sizeof(tmbuf), "%d:%02dAM", 
                      0 == t.hour ? 12 : t.hour, 
                      t.minute);
        } else {
            snprintf (tmbuf, sizeof(tmbuf), "%dAM", 
                      0 == t.hour ? 12 : t.hour);
        }
    }

    if (now.year != t.year) {
        /* full */
        return snprintf(buffer, buflen, "%s %s %d %4d %s",
                        weekdayShortNames[BongoCalTimeGetWeekday(t)], monthShortNames[t.month],
                        t.day, t.year,
                        tmbuf);
    } else if (twhen - tnow < 60 * 60 * 24 * 6 && tnow < twhen) {
        return snprintf(buffer, buflen, "%s %s",
                        weekdayShortNames[BongoCalTimeGetWeekday(t)], tmbuf);
    } else {
        return snprintf(buffer, buflen, "%s %s %d %s",
                        weekdayShortNames[BongoCalTimeGetWeekday(t)], monthShortNames[t.month], 
                        t.day, tmbuf);
    }
}

BongoCalTime
BongoCalTimeAdjust(BongoCalTime t, int days, int hours, int minutes, int seconds)
{
    int second, minute, hour, day;
    int minutesOverflow = 0, hoursOverflow = 0, daysOverflow = 0, yearsOverflow = 0;
    int daysInMonth;

    ASSERTTZ(t);

    if (!t.isDate) {
        /* Add on seconds */
        second = t.second + seconds;
        t.second = second % 60;
        minutesOverflow = second / 60;
        if (t.second < 0) {
            t.second += 60;
            minutesOverflow--;
        }
        
        /* Add on minutes */
        minute = t.minute + minutes + minutesOverflow;
        t.minute = minute % 60;
        hoursOverflow = minute / 60;
        if (t.minute < 0) {
            t.minute += 60;
            hoursOverflow--;
        }
        
        /* Add on hours */
        hour = t.hour + hours + hoursOverflow;
        t.hour = hour % 24;
        daysOverflow = hour / 24;
        if (t.hour < 0) {
            t.hour += 24;
            daysOverflow--;
        }
    }
    
    /* Normalize the month */
    if (t.month >= 12) {
        yearsOverflow = (t.month - 1) / 12;
        t.year += yearsOverflow;
        t.month -= yearsOverflow * 12;
    } else if (t.month < 0) {
        /* 0 to -11 is -1 year out, -12 to -23 is -2 years. */
        yearsOverflow = (t.month / 12) - 1;
        t.year += yearsOverflow;
        t.month -= yearsOverflow * 12;
    }

    /* Add on days */
    day = t.day + days + daysOverflow;
    if (day > 0) {
        while (1) {
            daysInMonth = BongoCalDaysInMonth(t.month, t.year);
            if (day <= daysInMonth) {
                break;
            }
            t.month++;
            if (t.month > 11) {
                t.year++;
                t.month = 0;
            }
            
            day -= daysInMonth;
        }
    } else {
        while (day <= 0) {
            if (t.month == 0) {
                t.year--;
                t.month = 11;
            } else {
                t.month--;
            }
            
            day += BongoCalDaysInMonth(t.month, t.year);
        }
    }
    
    t.day = day;

    assert(t.month >= 0 && t.month <= 11);

    ASSERTTZ(t);
    
    return t;
}

BongoCalTime 
BongoCalTimeAdjustDuration(BongoCalTime t, BongoCalDuration d)
{
    if (!d.isNeg) {
        return BongoCalTimeAdjust(t, 
                                 (d.weeks * 7) + d.days,
                                 d.hours,
                                 d.minutes,
                                 d.seconds);
    } else {
        return BongoCalTimeAdjust(t, 
                                 -((d.weeks * 7) + d.days),
                                 -d.hours,
                                 -d.minutes,
                                 -d.seconds);
    }                            
}


int
BongoCalTimeGetJulianDay(BongoCalTime t)
{
    int year;
    int days;
    int index;
    
    ASSERTTZ(t);

    if (t.year < 0 || t.month > 13) {
        return 0;
    }
    
    year = t.year - 1;
    
    days = year * 365;
    days += (year / 4);
    days -= (year / 100);
    days += (year / 400);
    
    index = BongoCalIsLeapYear(t.year);
    days += daysInYear[index][t.month];
    days += t.day;

    return days;
}

int
BongoCalTimeGetWeekday(BongoCalTime t)
{
    ASSERTTZ(t);

    return ((BongoCalTimeGetJulianDay(t) - 1) % 7) + 1;
}

int
BongoCalTimeGetDayOfYear(BongoCalTime t)
{
    int index;
    
    ASSERTTZ(t);

    index = BongoCalIsLeapYear (t.year) ? 1 : 0;
    
    return (daysInYear[index][t.month] + t.day);
}

BongoCalDuration 
BongoCalDurationParseIcal(const char *str)
{
    BongoCalDuration d = {0, 0, 0, 0, 0, 0};
    int len;
    BOOL beginFlag = FALSE;
    BOOL timeFlag = FALSE;
    BOOL dateFlag = FALSE;
    BOOL weekFlag = FALSE;
    int digits = -1;
    int scanSize = -1;
    int i;

    len = strlen(str);

    for (i = 0; i < len; i++) {
        char p;
        
        p = str[i];
        
        switch (p) {
        case '-' : 
            if (i != 0 || beginFlag == 1) {
                goto error;
            }
            d.isNeg = TRUE;
            break;
        case 'P' :
            if (i != 0 && i != 1) goto error;
            beginFlag = TRUE;
            break;
        case 'T' :
            timeFlag = TRUE;
            break;
        case '0' :
        case '1' :
        case '2' :
        case '3' :
        case '4' :
        case '5' :
        case '6' :
        case '7' :
        case '8' :
        case '9' :
            /* HACK - skip any more digits if the last one read has
             * not been assigned */
            if (digits != -1) {
                break;
            }
            
            if (!beginFlag) {
                goto error;
            }
            
            scanSize = sscanf(&str[i], "%d", &digits);
            if (scanSize == 0) {
                goto error;
            }
            break;
        case 'H':
            if (timeFlag == FALSE || weekFlag == TRUE ||d.hours !=0 || digits == -1) {
                goto error;
            }
            
            d.hours = digits; 
            digits = -1;
            break;
        case 'M': 
            if (timeFlag == FALSE || weekFlag == TRUE || d.minutes != 0 || digits == -1) {
                goto error;
            }
            
            d.minutes = digits; 
            digits = -1;
            break;
        case 'S':
            if (timeFlag == 0 || weekFlag == 1 || d.seconds != 0 || digits == -1) {
                goto error;
            }
            d.seconds = digits; 
            digits = -1;
            break;
        case 'W':
            if (timeFlag == 1 || dateFlag == 1 || d.weeks != 0 || digits == -1) {
                goto error;
            }
            
            weekFlag = 1;
            d.weeks = digits; 
            digits = -1;
            break;
        case 'D': 
            if (timeFlag == 1||weekFlag == 1 || d.days != 0 || digits == -1) {
                goto error;
            }
            
            dateFlag = 1;
            d.days = digits; 
            digits = -1;
            break;
        default:
            goto error;
        }
    }
    
    return d;
    
error:
    return emptyDuration;
}

