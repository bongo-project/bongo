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

#include <config.h>
#include <xpl.h>

#include <mdb.h>
#include <msgapi.h>
#include <msgdate.h>
#include <nmap.h>
#include "msgapip.h"

#include <libical/icaltime.h>
#include "bongoutil.h"

#define LAST 5

static unsigned long DTDaysAbsMonth[]   = { 0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334 };

#if 0 /* may use in future */
static unsigned long RataDieF[]         = { 306, 337, 0, 31, 61, 92, 122, 153, 184, 214, 245, 275 };
#endif

long ServerTimeZoneOffset = 0;

static long 
DSTSunday(unsigned long hour, unsigned long day, unsigned long month, unsigned long year,
          unsigned long startMonth, unsigned long endMonth,
          unsigned long startSunday, unsigned long endSunday,
          unsigned long startHour, unsigned long endHour,
          unsigned long ntz, unsigned long dtz)
{
    long rataDie;
    long dst;
    char dow;

    if (month == startMonth) {
        /* We might have DST; let's find out; first, get todays RataDie */
        rataDie = MsgGetRataDie(day, month, year);

        if (startSunday == LAST) {
            /* Now, calculate the rata die when DST starts */
            dst = MsgGetRataDie(1, (startMonth + 1), year);

            /* Find the proper day */
            dst--;
            dst -= (dst % 7);
        } else {
            /* Now, calculate the rata die when DST starts */
            dst = MsgGetRataDie(1, startMonth, year);

            /* Find the proper day */
            if ((dow = dst % 7)!=0) {
                dst += 7 - dow;
            }
            dst += (startSunday - 1) * 7;
        }

        /* Not yet dst? */
        if (rataDie <= dst) {
            if (rataDie == dst) {
                if (hour >= startHour) {
                    return(dtz * 60);
                }
            }
            return(ntz * 60);
        }
        return(dtz * 60);
    } else if (month == endMonth) {
        /* We might have DST; let's find out; first, get todays RataDie */
        rataDie = MsgGetRataDie(day, month, year);

        if (endSunday == LAST) {
            /* Now, calculate the rata die when DST ends */
            dst = MsgGetRataDie(1, (endMonth + 1), year);

            /* Find the proper day */
            dst--;
            dst -= (dst % 7);
        } else {
            /* Now, calculate the rata die when DST ends */
            dst = MsgGetRataDie(1, endMonth, year);

            /* Find the proper day */
            if ((dow = dst % 7)!=0) {
                dst += 7 - dow;
            }
            dst += (endSunday - 1) * 7;
        }

        /* Not yet dst? */
        if (rataDie <= dst) {
            if (rataDie == dst) {
                if (hour >= endHour) {
                    return(ntz * 60);
                }
            }
            return(dtz * 60);
        }
        return(ntz * 60);
    } else if ((startMonth < endMonth) && (month > startMonth) && (month < endMonth)) {
        /* Guaranteed to be daylight time */
        return(dtz * 60);
    } else if ((endMonth < startMonth) && ((month > startMonth) || (month < endMonth))) {
        /* Guaranteed to be daylight time */
        return(dtz * 60);
    } else {
        /* Guaranteed to be standard time */
        return(ntz * 60);
    }
}

EXPORT long
MsgGetRataDie(unsigned long day, unsigned long month, unsigned long year)
{
    /* Here's the formula:
     * STEP 3 The Rata Die is 
     * D+F+365*Z+Z\4-Z\100+Z\400 - 306
     * Therefore, a typical algorithm will contain: 
     *
     * IF m < 3 THEN 
     *           m = m + 12 
     *           y = y - 1 
     * END IF 
     *  rd = d + (153 * m - 457) \ 5 + 365 * y + y \ 4 - y \ 100 + y \ 400 - 306 */

    /* I should use the RataDieF... */
    if (month < 3) {
        month+=12;
        year--;
    }
    return(day + (153 * month - 457) / 5 + 365 * year + year/4 - year/100 + year/400 - 306);
}

EXPORT void
MsgGetDate(long rataDie, unsigned long *day, unsigned long *month, unsigned long *year)
{
    register long z;
    register long h;
    register long a;
    register long b;
    register long c;

    z = rataDie + 306;
    h = 100 * z - 25;
    a = h / 3652425;
    b = a - a / 4;
    *year = (100 * b + h) / 36525;
    c = b + z - 365 * (*year) - (*year) / 4;
    *month = (5 * c + 456) / 153;
    *day = c - (153 * (*month) - 457) / 5;
    if (*month > 12) {
        (*year)++;
        *month-=12;
    }

    return;    
}

EXPORT BOOL
MsgGetRFC822Date(long offset, long utcTime, unsigned char *dateBuffer)
{
    struct tm timeStruct;
    unsigned char tempBuffer[80];

    if (utcTime == 0) {
        utcTime = time(NULL);
    }

    if (offset == -1) {
        utcTime += ServerTimeZoneOffset;
        gmtime_r(&utcTime, &timeStruct);
        strftime(tempBuffer, 80, "%a, %d %b %Y %H:%M:%S %%+02.2ld%%02.2ld", &timeStruct);
        sprintf(dateBuffer, tempBuffer, (long)(ServerTimeZoneOffset / 3600), (long)((ServerTimeZoneOffset % 3600) / 60));
    } else {
        utcTime += offset;
        gmtime_r(&utcTime, &timeStruct);
        strftime(tempBuffer, 80, "%a, %d %b %Y %H:%M:%S %%+02.2ld%%02.2ld", &timeStruct);
        sprintf(dateBuffer, tempBuffer, (long)(offset / 3600), (long)((offset % 3600) / 60));
    }
    return(TRUE);
}

static int 
GetRFC822Offset(const char *offset, int *hours, int *minutes)
{
    if (offset[0] == '+') {
        sscanf(offset + 1, "%2d%2d", hours, minutes);
    } else if (offset[0] == '-') {
        sscanf(offset + 1, "%2d%2d", hours, minutes);
        *hours = 0 - *hours;
        *minutes = 0 - *minutes;
    } else if (!strcmp (offset, "UT") || !strcmp (offset, "GMT")) {
        *hours = 0;
        *minutes = 0;
    } else if (!strcmp(offset, "EST")) {
        *hours = -5;
        *minutes = 0;
    } else if (!strcmp(offset, "EDT")) {
        *hours = -4;
        *minutes = 0;
    } else if (!strcmp(offset, "CST")) {
        *hours = -6;
        *minutes = 0;
    } else if (!strcmp(offset, "CDT")) {
        *hours = -5;
        *minutes = 0;    
    } else if (!strcmp(offset, "MST")) {
        *hours = -7;
        *minutes = 0;
    } else if (!strcmp(offset, "MDT")) {
        *hours = -6;
        *minutes = 0;
    } else if (!strcmp(offset, "PST")) {
        *hours = -8;
        *minutes = 0;
    } else if (!strcmp(offset, "PDT")) {
        *hours = -7;
        *minutes = 0;
    } else {
        *hours = 0;
        *minutes = 0;
    }

    return 0;
}

unsigned long
MsgLookupRFC822Month(const char *name)
{
    static const char *monthNames[] = { "Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec" };
    int i;
    
    for (i = 0; i < 12; i++) {
        if (!strcmp(name, monthNames[i])) {
            return i + 1;
        }
    }
    
    return 0;
}

/* MsgParseRFC822Date() parses the same DateTime string as MsgParseRFC822DateTime() but it ignores time */
uint64_t
MsgParseRFC822Date(const char *date)
{
    uint64_t utc;
    char *dayPtr;
    char *monthPtr;
    char *yearPtr;
    unsigned long month;

    dayPtr = strchr(date, ',');
    if (dayPtr) {
        dayPtr++;
    } else {
        dayPtr = (char *)date;
    }

    while((*dayPtr == ' ') || (*dayPtr == '\t')) {
        dayPtr++;
    }

    monthPtr = strchr(dayPtr, ' ');
    if (monthPtr) {
        monthPtr++;
        yearPtr = strchr(monthPtr, ' ');
        if (yearPtr) {
            *yearPtr = '\0';
            month = MsgLookupRFC822Month(monthPtr);
            *yearPtr = ' ';
            yearPtr++;
            utc = MsgGetUTC((unsigned long)atol(dayPtr), month, (unsigned long)atol(yearPtr), 0, 0, 0);
            if (utc < ((unsigned long) -1)) {
                return(utc);
            }
        }
    }

    return 0;
}

uint64_t
MsgParseRFC822DateTime(const char *date)
{
    /* [Fri,] 20 01 05 21:05:00 EST */
    char *copy;
    char *pieces[7];
    char *timePieces[3];
    int count;
    int i;
    struct icaltimetype tm;
    int hourOffset;
    int minuteOffset;

    memset(&tm, 0, sizeof(struct icaltimetype));

    copy = MemStrdup(date);
    count = BongoStringSplit(copy, ' ', pieces, 7);
    
    if (count < 6) {
        MemFree(copy);
        return 0;
    }

    i = 0;
    if (!isdigit(pieces[0][0])) {
        /* Tue, */
        i++;
    }

    if ((count - i) < 5) {
        MemFree(copy);
        return 0;
    }
    
    tm.day = atoi(pieces[i++]);
    tm.month = MsgLookupRFC822Month(pieces[i++]);
    if (tm.month == 0) {
        MemFree(copy);
        return 0;
    }

    tm.year = atoi(pieces[i++]);

    if (tm.year < 100) {
        tm.year += 1900;
    }

    tm.is_date = 0;

    count = BongoStringSplit(pieces[i++], ':', timePieces, 3);
    if (count < 2 || count > 3) {
        MemFree(copy);
        return 0;
    }
    
    tm.hour = atoi(timePieces[0]);
    tm.minute = atoi(timePieces[1]);
    tm.second = count == 3 ? atoi(timePieces[2]) : 0;
    
    GetRFC822Offset(pieces[i++], &hourOffset, &minuteOffset);

    tm.hour -= hourOffset;
    tm.minute -= minuteOffset;
    
    MemFree(copy);

    return (uint64_t)icaltime_as_timet(tm);   
}


EXPORT long
MsgGetUTCOffset(void)
{
/*    tzset();*/
    return((long)ServerTimeZoneOffset);
}

EXPORT unsigned long
MsgGetUTC(unsigned long day, unsigned long month, unsigned long year, unsigned long hour, unsigned long minute, unsigned long second)
{
    unsigned long utc;

    if ((day) && (day < 32)) {
        if ((month) && (month < 13)) {
            if ((year > 1969) && (year < 2038)) {
                /* Calculate years into days; consider leap year */
                utc = (year-1970) * 365 + ((year -1901) >> 2) - 17;

                if (month>1) {
                    utc += DTDaysAbsMonth[month - 1];
                }

                if (MSG_IS_LEAP_YEAR(year)) {
                    if (month > 2) {
                        utc++;
                    }
                }
                utc += day - 1;

                /* We now have the date in days */
                utc *= 24 * 60 * 60;
                /* Now we're in seconds */
                utc += hour * 60 * 60;
                utc += minute * 60;
                utc += second;

                return(utc);
            }
        }
    }

    return((unsigned long) - 1);
}

EXPORT BOOL
MsgGetDMY(unsigned long utcTime, unsigned long *day, unsigned long *month, unsigned long *year, unsigned long *hour, unsigned long *minute, unsigned long *second)
{
    struct tm timeStruct;

    gmtime_r((time_t *)&utcTime, &timeStruct);
    if (day) {
        *day = timeStruct.tm_mday;
    }
    if (month) {
        *month = timeStruct.tm_mon+1;
    }
    if (year) {
        *year = timeStruct.tm_year+1900;
    }
    if (hour) {
        *hour = timeStruct.tm_hour;
    }
    if (minute) {
        *minute = timeStruct.tm_min;
    }
    if (second) {
        *second = timeStruct.tm_sec;
    }

    return(TRUE);
}

/* We support:
 *  %a - Locales abbreviated weekday name
 *  %A - Locales full weekday name
 *  %b - Locales abbreviated month name
 *  %B - Locales full month name
 *  %d - Day of month (01-31)
 *  %H - Hour (24 hour) 00-23
 *  %I    - Hour (12 hour) 01-12
 *  %j - Day of the year
 *  %m - Month as decimal number (01-12)
 *  %M - Minute as decimal number (00-59)
 *  %n    - Newline character
 *  %p - Locale equivalent of either am or pm
 *  %r - 12 hour time representation hh:mm:ss [am/pm]
 *  %S - Second as decimal number (00-59)
 *  %t    - Tab character
 *  %T - 24 hour time representation HH:MM:SS
 *  %U - Week number of the year
 *  %w - Weekday as decimal number; 0=sunday
 *  %y - Year without century
 *  %Y - Year with century as decimal number
 *  %Z - Timezone offset from UTC (+/- 0000) */

EXPORT unsigned long
MsgPrint(unsigned char *buffer, int bufSize, unsigned char *format, unsigned long timeIn, MsgDateFormat *dateInfo)
{
    unsigned long rataDie;
    unsigned char *src;
    unsigned char *dest;
    unsigned char *limit;
    struct tm tms;
    unsigned long i;

    rataDie = 0;
    src = format;
    dest = buffer;
    limit = buffer + bufSize;

    gmtime_r((time_t *)&timeIn, &tms);

    while (*src && dest < limit) {
        switch (*src) {
	case '%': {
	    src++;
	    switch (*src) {
	    case 'a': {
		i = strlen(dateInfo->wDayShort[tms.tm_wday]);
		if (i > (unsigned long)(limit - dest)) {
		    i = limit - dest;
		}
			
		memcpy(dest, dateInfo->wDayShort[tms.tm_wday], i);
		dest += i;
		src++;
		continue;
	    }

	    case 'A': {
		i = strlen(dateInfo->wDayLong[tms.tm_wday]);
		if (i > (unsigned long)(limit - dest)) {
		    i = limit - dest;
		}
		memcpy(dest, dateInfo->wDayLong[tms.tm_wday], i);
		dest += i;
		src++;
		continue;
	    }

	    case 'b': {
		i = strlen(dateInfo->monthShort[tms.tm_mon]);
		if (i > (unsigned long)(limit - dest)) {
		    i = limit - dest;
		}
		memcpy(dest, dateInfo->monthShort[tms.tm_mon], i);
		dest += i;
		src++;
		continue;
	    }

	    case 'B': {
		i = strlen(dateInfo->monthLong[tms.tm_mon]);
		if (i > (unsigned long)(limit - dest)) {
		    i = limit - dest;
		}
		memcpy(dest, dateInfo->monthLong[tms.tm_mon], i);
		dest += i;
		src++;
		continue;
	    }

	    case 'd': {
		dest += snprintf(dest, limit - dest, "%02d", tms.tm_mday);
		src++;
		continue;
	    }

	    case 'H': {
		dest += snprintf(dest, limit - dest, "%02d", tms.tm_hour);
		src++;
		continue;
	    }

	    case 'I': {
		if (tms.tm_hour!=0) {
		    if (tms.tm_hour>12) {
			dest += snprintf(dest, limit - dest, "%02d", tms.tm_hour - 12);
		    } else {
			dest += snprintf(dest, limit - dest, "%02d", tms.tm_hour);
		    }
		} else {
		    i = limit - dest > 2 ? 2 : limit - dest;
		    memcpy(dest, "12", i);
		    dest += i;
		}
		src++;
		continue;
	    }

	    case 'j': {
		dest += snprintf(dest, limit - dest, "%d", tms.tm_yday + 1);
		src++;
		continue;
	    }

	    case 'm': {
		dest += snprintf (dest, limit - dest, "%02d", tms.tm_mon + 1);
		src++;
		continue;
	    }

	    case 'M': {
		dest += snprintf(dest, limit - dest, "%02d", tms.tm_min);
		src++;
		continue;
	    }

	    case 'n': {
		*dest++ = '\n';
		src++;
		continue;
	    }

	    case 'p': {
		dest += snprintf(dest, limit - dest, "%s", tms.tm_hour > 11 ? dateInfo->AmPm[1] : dateInfo->AmPm[0]);
		src++;
		continue;
	    }

	    case 'r': {
		if (tms.tm_hour!=0) {
		    if (tms.tm_hour>12) {
			dest += snprintf(dest, limit - dest, "%02d:%02d:%02d %s", tms.tm_hour-12, tms.tm_min, tms.tm_sec, dateInfo->AmPm[1]);
		    } else {
			dest += snprintf(dest, limit - dest, "%02d:%02d:%02d %s", tms.tm_hour, tms.tm_min, tms.tm_sec, tms.tm_hour > 11 ? dateInfo->AmPm[1] : dateInfo->AmPm[0]);
		    }
		} else {
		    dest += snprintf(dest, limit - dest, "12:%02d:%02d %s", tms.tm_min, tms.tm_sec, dateInfo->AmPm[0]);
		}
		src++;
		continue;
	    }

	    case 'S': {
		dest += snprintf(dest, limit - dest, "%02d", tms.tm_sec);
		src++;
		continue;
	    }

	    case 't': {
		*dest++ = 0x09;
		src++;
		continue;
	    }

	    case 'T': {
		dest += snprintf(dest, limit - dest, "%02d:%02d:%02d", tms.tm_hour, tms.tm_min, tms.tm_sec);
		src++;
		continue;
	    }

	    case 'U': {
		unsigned long rataDieYearStart;
		unsigned long rataDieToday;

		rataDieYearStart = MsgGetRataDie(1, 1, tms.tm_year + 1900);
		rataDieToday = MsgGetRataDie(tms.tm_mday, tms.tm_mon + 1, tms.tm_year + 1900);
		i = rataDieYearStart % 7;
		if (i <= dateInfo->wDayStart) {
		    rataDieYearStart -= abs(dateInfo->wDayStart - 7);
		}
		rataDieYearStart -= i;
		dest += snprintf(dest, limit - dest, "%d", (int)((rataDieToday-rataDieYearStart) / 7 + 1));
		src++;
		continue;
	    }

	    case 'w': {
		dest += snprintf(dest, limit - dest, "%d", tms.tm_wday);
		src++;
		continue;
	    }

	    case 'y': {
		dest += snprintf(dest, limit - dest, "%02d", tms.tm_year > 99 ? tms.tm_year - 100 : tms.tm_year);
		src++;
		continue;
	    }

	    case 'Y': {
		dest += snprintf(dest, limit - dest, "%04d", tms.tm_year + 1900);
		src++;
		continue;
	    }

	    case 'Z': {
		dest += snprintf(dest, limit - dest, "%+4.4ld", dateInfo->timezoneOffset/36);
		src++;
		continue;
	    }

	    default: {
		*dest++ = *src++;
		continue;
	    }
	    }
	}
	default: {
	    *dest++=*src++;
	    continue;
	}
        }
    }

    return(dest - buffer);
}


EXPORT unsigned long
MsgGetTimezoneID(long day, long wDay, unsigned long month, unsigned long hour,
                 long dstDay, long dstWDay, unsigned long dstMonth, unsigned long dstHour,
                 long offset, long dstOffset)
{
    if (day == dstDay && month == dstMonth && hour == dstHour) {
        /* No timezone switching */
        switch(offset) {
	case 0:      return(TZ_GREENWICH);
	case 60:     return(TZ_WEST_CENTRAL_AFRICA);
	case 120:    return(TZ_SOUTH_AFRICA);
	case 180:    return(TZ_ARAB);
	case 240:    return(TZ_ARABIAN);
	case 270:    return(TZ_AFGHANISTAN);
	case 300:    return(TZ_WEST_ASIA);
	case 330:    return(TZ_INDIA);
	case 345:    return(TZ_NEPAL);
	case 360:    return(TZ_CENTRAL_ASIA);
	case 390:    return(TZ_MYANMAR);
	case 420:    return(TZ_SOUTH_EAST_ASIA);
	case 480:    return(TZ_CHINA);
	case 540:    return(TZ_TOKYO);
	case 570:    return(TZ_AUS_CENTRAL);
	case 600:    return(TZ_EAST_AUSTRALIA);
	case 660:    return(TZ_CENTRAL_PACIFIC);
	case 720:    return(TZ_FIJI);
	case 780:    return(TZ_TONGO);
	case -60:    return(TZ_CAPE_VERDE);
	case -180:   return(TZ_SA_EASTERN);
	case -240:   return(TZ_SA_WESTERN);
	case -300:   return(TZ_US_EASTERN);
	case -360:   return(TZ_CENTRAL_AMERICA);
	case -420:   return(TZ_US_MOUNTAIN);
	case -600:   return(TZ_HAWAIIAN);
	case -660:   return(TZ_SAMOA);
	case -720:   return(TZ_DATELINE);
	default:     return(TZ_GREENWICH);
        }
    }

    /* We got here, so there must be some kind of DST */

    /* Check the fixed date switchers */
    if (offset == 120 && dstOffset == 180 && 
	month == 10 && dstMonth == 5 &&
	day == 30 && dstDay == 1 &&
	hour == 0 && dstHour == 0 ) {
            
        return(TZ_EGYPT);
    }

    if (offset == 180 && dstOffset == 240 && 
	month == 10 && dstMonth == 4 &&
	day==30 && dstDay==1 &&
	hour==0 && dstHour==0 ) {
            
        return(TZ_ARABIC);
    }

    /* Now we have the last week of month begin and end on sunday */
    if (wDay==0 && dstWDay==0 && day==-1 && dstDay==-1) {
        if (dstMonth==3 && month==10 && dstHour==1 && hour==2 && offset==0 && dstOffset==60)    return(TZ_GMT);
        if (dstMonth==3 && month==10 && dstHour==2 && hour==3 && offset==60 && dstOffset==120)  return(TZ_WEST_EUROPE);
        if (dstMonth==3 && month==10 && dstHour==2 && hour==3 && offset==120 && dstOffset==180) return(TZ_GBT);
        if (dstMonth==3 && month==9 && dstHour==0 && hour==1 && offset==120 && dstOffset==180)  return(TZ_EAST_EUROPE);
        if (dstMonth==3 && month==10 && dstHour==3 && hour==4 && offset==120 && dstOffset==180) return(TZ_FLE);
        if (dstMonth==3 && month==10 && dstHour==2 && hour==3 && offset==180 && dstOffset==240) return(TZ_RUSSIAN);
        if (dstMonth==3 && month==10 && dstHour==2 && hour==3 && offset==240 && dstOffset==300) return(TZ_CAUCASUS);
        if (dstMonth==3 && month==10 && dstHour==2 && hour==3 && offset==300 && dstOffset==360) return(TZ_EKATERINENBURG);
        if (dstMonth==3 && month==10 && dstHour==2 && hour==3 && offset==360 && dstOffset==420) return(TZ_NORTH_CENTRAL_ASIA);
        if (dstMonth==3 && month==10 && dstHour==2 && hour==3 && offset==420 && dstOffset==480) return(TZ_NORTH_EAST_ASIA);
        if (dstMonth==3 && month==10 && dstHour==2 && hour==3 && offset==480 && dstOffset==540) return(TZ_NORTH_ASIA_EAST);
        if (dstMonth==3 && month==10 && dstHour==2 && hour==3 && offset==540 && dstOffset==600) return(TZ_YAKUTSK);
        if (dstMonth==3 && month==10 && dstHour==2 && hour==3 && offset==600 && dstOffset==660) return(TZ_VLADIVOSTOK);
        if (dstMonth==3 && month==10 && dstHour==2 && hour==3 && offset==-60 && dstOffset==0)   return(TZ_AZORES);
        if (dstMonth==3 && month==9 && dstHour==2 && hour==3 && offset==-120 && dstOffset==-60) return(TZ_MID_ATLANTIC);
        if (dstMonth==10 && month==3 && dstHour==2 && hour==3 && offset==570 && dstOffset==630) return(TZ_CENTRAL_AUSTRALIA);
        if (dstMonth==10 && month==3 && dstHour==2 && hour==3 && offset==600 && dstOffset==660) return(TZ_AUS_EASTERN);
    }

    /* Now the ones that switch to dst on the first sunday and from dst on the last sunday */
    if (wDay==0 && dstWDay==0 && day==-1 && dstDay==1) {
        if (dstMonth==10 && month==3 && dstHour==2 && hour==2 && offset==600 && dstOffset==660)   return(TZ_TASMANIA);
        if (dstMonth==4 && month==10 && dstHour==2 && hour==2 && offset==-180 && dstOffset==-120) return(TZ_GREENLAND);
        if (dstMonth==4 && month==10 && dstHour==2 && hour==2 && offset==-210 && dstOffset==-150) return(TZ_NEWFOUNDLAND);
        if (dstMonth==4 && month==10 && dstHour==2 && hour==2 && offset==-240 && dstOffset==-180) return(TZ_ATLANTIC);
        if (dstMonth==4 && month==10 && dstHour==2 && hour==2 && offset==-300 && dstOffset==-240) return(TZ_EASTERN);
        if (dstMonth==4 && month==10 && dstHour==2 && hour==2 && offset==-360 && dstOffset==-300) return(TZ_CENTRAL);
        if (dstMonth==4 && month==10 && dstHour==2 && hour==2 && offset==-420 && dstOffset==-360) return(TZ_MOUNTAIN);
        if (dstMonth==4 && month==10 && dstHour==2 && hour==2 && offset==-480 && dstOffset==-420) return(TZ_PACIFIC);
        if (dstMonth==4 && month==10 && dstHour==2 && hour==2 && offset==-540 && dstOffset==-480) return(TZ_ALASKAN);
    }

    /* New Zealand switches to dst on the first sunday and from dst on the third sunday */
    if (wDay==0 && dstWDay==0 && day==3 && dstDay==1) {
        if (dstMonth==10 && month==3 && dstHour==2 && hour==2 && offset==720 && dstOffset==780)   return(TZ_NEW_ZEALAND);
    }

    /* If we make it to here, we've got some special cases */
    /* FIXME Handle TZ_EAST_SOUTH_AMERICA, TZ_PACIFIC_SA and TZ_IRAN */
    return(TZ_GMT);
}

/* There are several different ways how DST is calculated; 
 * the comments below will show for each timezone.
 *
 * In general, the most common is to have a month and a week
 * and a particular day of that week. Week 5 means the last 
 * week of the month, which makes it a bit more complicated. */

EXPORT long
MsgGetUTCOffsetByDate(unsigned long timezone, unsigned long day, unsigned long month, unsigned long year, unsigned long hour)
{
    if (year == 0) {
        struct tm tms;
        time_t curtime;

        curtime = time(NULL);
        gmtime_r(&curtime, &tms);
        year = tms.tm_year+1900;
        month = tms.tm_mon+1;
        day = tms.tm_mday;
        hour = tms.tm_hour;
    }
    /* There are a few generic ways of calculating; 
     * we grab the variables that are important for 
     * each method and then drop into the method */

    switch(timezone) {
        /* No switching at all */
    case TZ_GREENWICH:           return(0);
    case TZ_WEST_CENTRAL_AFRICA: return(60*60);
    case TZ_SOUTH_AFRICA:        return(120*60);
    case TZ_ISRAEL:              return(120*60);
    case TZ_ARAB:                return(180*60);
    case TZ_EAST_AFRICA:         return(180*60);
    case TZ_ARABIAN:             return(240*60);
    case TZ_AFGHANISTAN:         return(270*60);
    case TZ_WEST_ASIA:           return(300*60);
    case TZ_INDIA:               return(330*60);
    case TZ_NEPAL:               return(345*60);
    case TZ_CENTRAL_ASIA:        return(360*60);
    case TZ_SRI_LANKA:           return(360*60);
    case TZ_MYANMAR:             return(390*60);
    case TZ_SOUTH_EAST_ASIA:     return(420*60);
    case TZ_CHINA:               return(480*60);
    case TZ_SINGAPORE:           return(480*60);
    case TZ_WEST_AUSTRALIA:      return(480*60);
    case TZ_TAIPEI:              return(480*60);
    case TZ_TOKYO:               return(540*60);
    case TZ_KOREA:               return(540*60);
    case TZ_AUS_CENTRAL:         return(570*60);
    case TZ_EAST_AUSTRALIA:      return(600*60);
    case TZ_WEST_PACIFIC:        return(600*60);
    case TZ_CENTRAL_PACIFIC:     return(660*60);
    case TZ_FIJI:                return(720*60);
    case TZ_TONGO:               return(780*60);
    case TZ_CAPE_VERDE:          return(-60*60);
    case TZ_SA_EASTERN:          return(-180*60);
    case TZ_SA_WESTERN:          return(-240*60);
    case TZ_SA_PACIFIC:          return(-300*60);
    case TZ_US_EASTERN:          return(-300*60);
    case TZ_CENTRAL_AMERICA:     return(-360*60);
    case TZ_CANADA_CENTRAL:      return(-360*60);
    case TZ_US_MOUNTAIN:         return(-420*60);
    case TZ_HAWAIIAN:            return(-600*60);
    case TZ_SAMOA:               return(-660*60);
    case TZ_DATELINE:            return(-720*60);

        /* Fixed-Date based switching */
    case TZ_EGYPT: {
	if (month < 5 || month > 9 || (month == 9 && day == 30)) {
	    return(120 * 60);
	}
	return(180 * 60);
    }

    case TZ_ARABIC: {
	if (month < 4 || month > 9 || (month == 9 && day == 30)) {
	    return(180 * 60);
	}
	return(240 * 60);
    }

        /* Last week of month begin and end method */
    case TZ_GMT:              
	    return DSTSunday(hour, day, month, year, 3, 10, LAST, LAST, 1, 2, 0, 60);
    case TZ_WEST_EUROPE:
    case TZ_CENTRAL_EUROPE:
    case TZ_ROMANCE:
    case TZ_CENTRAL_EUROPEAN: 
	return DSTSunday(hour, day, month, year, 3, 10, LAST, LAST, 2, 3, 60, 120);
    case TZ_GBT:                        
	return DSTSunday(hour, day, month, year, 3, 10, LAST, LAST, 2, 3, 120, 180);
    case TZ_EAST_EUROPE:                
	return DSTSunday(hour, day, month, year, 3, 9,  LAST, LAST, 0, 1, 120, 180);
    case TZ_FLE:                        
	return DSTSunday(hour, day, month, year, 3, 10, LAST, LAST, 3, 4, 120, 180);
    case TZ_RUSSIAN:                    
	return DSTSunday(hour, day, month, year, 3, 10, LAST, LAST, 2, 3, 180, 240);
    case TZ_CAUCASUS:                    
	return DSTSunday(hour, day, month, year, 3, 10, LAST, LAST, 2, 3, 240, 300);
    case TZ_EKATERINENBURG:            
	return DSTSunday(hour, day, month, year, 3, 10, LAST, LAST, 2, 3, 300, 360);
    case TZ_NORTH_CENTRAL_ASIA:    
	return DSTSunday(hour, day, month, year, 3, 10, LAST, LAST, 2, 3, 360, 420);
    case TZ_NORTH_EAST_ASIA:        
	return DSTSunday(hour, day, month, year, 3, 10, LAST, LAST, 2, 3, 420, 480);
    case TZ_NORTH_ASIA_EAST:        
	return DSTSunday(hour, day, month, year, 3, 10, LAST, LAST, 2, 3, 480, 540);
    case TZ_YAKUTSK:                    
	return DSTSunday(hour, day, month, year, 3, 10, LAST, LAST, 2, 3, 540, 600);
    case TZ_VLADIVOSTOK:                
	return DSTSunday(hour, day, month, year, 3, 10, LAST, LAST, 2, 3, 600, 660);
    case TZ_AZORES:                    
	return DSTSunday(hour, day, month, year, 3, 10, LAST, LAST, 2, 3, -60, 0);
    case TZ_MID_ATLANTIC:
	return DSTSunday(hour, day, month, year, 3, 9, LAST, LAST, 2, 3, -120, -60);        
    case TZ_GREENLAND:
	return DSTSunday(hour, day, month, year, 4, 10, 1, LAST, 2, 2, -180, -120);
    case TZ_NEWFOUNDLAND:
	return DSTSunday(hour, day, month, year, 4, 10, 1, LAST, 2, 2, -210, -150);
    case TZ_ATLANTIC:
	return DSTSunday(hour, day, month, year, 4, 10, 1, LAST, 2, 2, -240, -180);
    case TZ_EASTERN:
	return DSTSunday(hour, day, month, year, 4, 10, 1, LAST, 2, 2, -300, -240);
    case TZ_CENTRAL:
	return DSTSunday(hour, day, month, year, 4, 10, 1, LAST, 2, 2, -360, -300);
    case TZ_MEXICO:                    
	return DSTSunday(hour, day, month, year, 4, 10, 1, LAST, 2, 2, -360, -300);
    case TZ_MOUNTAIN:
	return DSTSunday(hour, day, month, year, 4, 10, 1, LAST, 2, 2, -420, -360);
    case TZ_PACIFIC:
	return DSTSunday(hour, day, month, year, 4, 10, 1, LAST, 2, 2, -480, -420);
    case TZ_ALASKAN:
	return DSTSunday(hour, day, month, year, 4, 10, 1, LAST, 2, 2, -540, -480);
    case TZ_CENTRAL_AUSTRALIA:        
	return DSTSunday(hour, day, month, year, 10, 3, LAST, LAST, 2, 3, 570, 630);
    case TZ_AUS_EASTERN:
	return DSTSunday(hour, day, month, year, 10, 3, LAST, LAST, 2, 3, 600, 660);
    case TZ_TASMANIA:
	return DSTSunday(hour, day, month, year, 10, 3, 1, LAST, 2, 2, 600, 660);
    case TZ_NEW_ZEALAND:
	return DSTSunday(hour, day, month, year, 10, 3, 1, 3, 2, 2, 720, 780);
        /* All the special cases */
    case TZ_IRAN:
	if (month >= 3 && month <= 9) {
            long rataDie;
            long dst;

	    /* We might have DST; let's find out; first, get todays RataDie */
	    rataDie = MsgGetRataDie(day, month, year);

	    /* Now, calculate the rata die when DST starts */
	    dst = MsgGetRataDie(1, 3, year);
                                                                                                                        
	    /* Find the proper day */
	    dst += dst % 7;

	    /* Not yet DST? */
	    if (rataDie <= dst) {
	        if (rataDie == dst) {
	            if (hour >= 2) {
	                return(270 * 60);
	            }
	        }
	        return(210 * 60);
	    }

	    /* Calculate the end of DST */
	    dst = MsgGetRataDie(1, 9, year);

	    /* Fourth week, 2 dow */
	    dst += 3 * 7;
	    dst -= dst % 7;
	    dst += 2;

	    if (rataDie <= dst) {
	        if (rataDie == dst) {
	            if (hour >= 2) {
	                return(210 * 60);
	            }
	        }
	        return(270 * 60);
	    } else {
	        return(210 * 60);
	    }
	} else {
	    /* Guaranteed to be standard time */
	    return(210 * 60);
	}
    case TZ_EAST_SOUTH_AMERICA:
    case TZ_PACIFIC_SA:    
	return(0);
    }

    return(0);
}

EXPORT long
MsgGetUTCOffsetByUTC(unsigned long timezone, unsigned long utc)
{
    unsigned long day;
    unsigned long month;
    unsigned long year;
    unsigned long hour;
    struct tm tms;

    gmtime_r(&utc, &tms);
    year = tms.tm_year+1900;
    month = tms.tm_mon+1;
    day = tms.tm_mday;
    hour = tms.tm_hour;

    return MsgGetUTCOffsetByDate(timezone, day, month, year, hour);
}

void 
MsgDateSetUTCOffset(long offset)
{
    ServerTimeZoneOffset = offset;

    return;
}

BOOL
MsgDateStart(void)
{
    tzset();

    return(TRUE);
}
