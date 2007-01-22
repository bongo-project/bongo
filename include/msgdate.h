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

#ifndef MSGDATE_H
#define MSGDATE_H

/* This is our Date conversion/calculation library.
 * We calculate everything in the "rata die" format, since this
 * allows to easily calculate "Day of Week" functionality.
 * The calculations/formulas I used can be found at
 * http://www.capecod.net/~pbaum/date/date0.htm
 * http://www.capecod.net/~pbaum/date/rata.htm
 * http://www.capecod.net/~pbaum/date/inrata.htm
 */

/* Timezones we understand */
#define TZ_UTC                  0
#define TZ_GREENWICH            1
#define TZ_GMT                  2
#define TZ_WEST_EUROPE          3
#define TZ_CENTRAL_EUROPE       4
#define TZ_ROMANCE              5
#define TZ_CENTRAL_EUROPEAN     6
#define TZ_WEST_CENTRAL_AFRICA  7
#define TZ_GBT                  8
#define TZ_EAST_EUROPE          9
#define TZ_EGYPT                10
#define TZ_SOUTH_AFRICA         11
#define TZ_FLE                  12
#define TZ_ISRAEL               13
#define TZ_ARABIC               14
#define TZ_ARAB                 15
#define TZ_RUSSIAN              16
#define TZ_EAST_AFRICA          17
#define TZ_IRAN                 18
#define TZ_ARABIAN              19
#define TZ_CAUCASUS             20
#define TZ_AFGHANISTAN          21
#define TZ_EKATERINENBURG       22
#define TZ_WEST_ASIA            23
#define TZ_INDIA                24
#define TZ_NEPAL                25
#define TZ_NORTH_CENTRAL_ASIA   26
#define TZ_CENTRAL_ASIA         27
#define TZ_SRI_LANKA            28
#define TZ_MYANMAR              29
#define TZ_SOUTH_EAST_ASIA      30
#define TZ_NORTH_EAST_ASIA      31
#define TZ_CHINA                32
#define TZ_NORTH_ASIA_EAST      33
#define TZ_SINGAPORE            34
#define TZ_WEST_AUSTRALIA       35
#define TZ_TAIPEI               36
#define TZ_TOKYO                37
#define TZ_KOREA                38
#define TZ_YAKUTSK              39
#define TZ_CENTRAL_AUSTRALIA    40
#define TZ_AUS_CENTRAL          41
#define TZ_EAST_AUSTRALIA       42
#define TZ_AUS_EASTERN          43
#define TZ_WEST_PACIFIC         44
#define TZ_TASMANIA             45
#define TZ_VLADIVOSTOK          46
#define TZ_CENTRAL_PACIFIC      47
#define TZ_NEW_ZEALAND          48
#define TZ_FIJI                 49
#define TZ_TONGO                50
#define TZ_AZORES               51
#define TZ_CAPE_VERDE           52
#define TZ_MID_ATLANTIC         53
#define TZ_EAST_SOUTH_AMERICA   54
#define TZ_SA_EASTERN           55
#define TZ_GREENLAND            56
#define TZ_NEWFOUNDLAND         57
#define TZ_ATLANTIC             58
#define TZ_SA_WESTERN           59
#define TZ_PACIFIC_SA           60
#define TZ_SA_PACIFIC           61
#define TZ_EASTERN              62
#define TZ_US_EASTERN           63
#define TZ_CENTRAL_AMERICA      64
#define TZ_CENTRAL              65
#define TZ_MEXICO               66
#define TZ_CANADA_CENTRAL       67
#define TZ_US_MOUNTAIN          68
#define TZ_MOUNTAIN             69
#define TZ_PACIFIC              70
#define TZ_ALASKAN              71
#define TZ_HAWAIIAN             72
#define TZ_SAMOA                73
#define TZ_DATELINE             74

typedef struct _MsgDateFormat {
    unsigned char **monthShort;
    unsigned char **monthLong;
    unsigned char **wDayShort;
    unsigned char **wDayLong;
    unsigned char **AmPm;

    unsigned long wDayStart;

    long timezoneOffset;
} MsgDateFormat;

/*  fixme - this is hokey */
#ifdef MSGDATE_H_NEED_DAYS_PER_MONTH
static unsigned long MsgDaysPerMonth[] = { 31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31 };
#endif

#define MSG_IS_LEAP_YEAR(y)         (((((y)) % 100) != 0 && (((y)) % 4) == 0) || (((y)) % 400) == 0)
#define MSG_DAYS_IN_MONTH(m, y)     (MsgDaysPerMonth[((m)) - 1] + ((((m)) == 2 && MSG_IS_LEAP_YEAR((y)) == 1) ? 1 : 0))
#define MSG_DAY_OF_WEEK(r)          ((r) % 7)
#define MSG_CAL_ROWS(s, d, m, y, r) ((MSG_DAYS_IN_MONTH(((m)), ((y))) - 1 + ((MSG_DAY_OF_WEEK((r)) - (s) + 7) % 7 + 36 - ((d))) % 7) / 7 + 1)

#ifdef __cplusplus
extern "C" {
#endif

/* The real functions */
EXPORT void MsgGetDate(long rataDie, unsigned long *day, unsigned long *month, unsigned long *year);
EXPORT long MsgGetRataDie(unsigned long day, unsigned long month, unsigned long year);

/* Date Interface */
EXPORT BOOL MsgGetRFC822Date(long offset, long utcCime, unsigned char *dateBuffer);
EXPORT long MsgGetUTCOffset(void);

EXPORT unsigned long MsgGetUTC(unsigned long day, unsigned long month, unsigned long year, unsigned long hour, unsigned long minute, unsigned long second);
EXPORT BOOL MsgGetDMY(unsigned long utcTime, unsigned long *day, unsigned long *month, unsigned long *year, unsigned long *hour, unsigned long *minute, unsigned long *second);
EXPORT unsigned long MsgGetTimezoneID(long day, long wDay, unsigned long month, unsigned long hour, long dstDay, long dstWDay, unsigned long dstMonth, unsigned long dstHour, long offset, long dstOffset);
EXPORT long MsgGetUTCOffsetByDate(unsigned long timezone, unsigned long day, unsigned long month, unsigned long year, unsigned long hour);
EXPORT long MsgGetUTCOffsetByUTC(unsigned long timezone, unsigned long utc);
EXPORT unsigned long MsgPrint(unsigned char *buffer, int bufLen, unsigned char *format, unsigned long time, MsgDateFormat *dateInfo);

unsigned long MsgLookupRFC822Month(const char *name);
uint64_t MsgParseRFC822DateTime(const char *date);
uint64_t MsgParseRFC822Date(const char *date);


#ifdef __cplusplus
}
#endif

#endif /* MSGDATE_H */
