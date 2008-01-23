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

#include <logger.h>

#include <msgapi.h>

#include "imapd.h"

#define GREATER_THAN_OR_EQUAL_TO 1
#define LESS_THAN_OR_EQUAL_TO -1
#define EXACTLY_EQUAL_TO 0

__inline static long
FindClosestUid(long matchType, unsigned long uid,  MessageInformation* message, unsigned long firstMessage, unsigned long lastMessage)
{
    register unsigned long start;
    register unsigned long end;
    register unsigned long mean;
    unsigned long newMean;

    start = firstMessage;
    end = lastMessage + 1;
    mean = start + ((end - start) >> 1);

    for (;;) {
        if (uid < message[mean].uid) {
            end = mean;
        } else if (uid > message[mean].uid) {
            start = mean;
        } else {
            return(mean);
        }
                   
        newMean = start + ((end - start) >> 1);
        if (newMean != mean) {
            mean = newMean;
            continue;
        }
        break;
    }

    if (matchType == GREATER_THAN_OR_EQUAL_TO) {
        return(end);
    } 

    if (matchType == LESS_THAN_OR_EQUAL_TO) {
        return(start);
    } 

    return(-1);
}

long
UidToSequenceNum(MessageInformation *message, unsigned long messageCount, unsigned long uid, unsigned long *sequenceNum)
{
    long ccode;

    ccode = FindClosestUid(EXACTLY_EQUAL_TO, uid, message, 0, messageCount - 1);
    if (ccode != -1) { 
        *sequenceNum = ccode;
        return(STATUS_CONTINUE);
    }
    return(STATUS_UID_NOT_FOUND);
}


__inline static long
UidToSequenceRange(MessageInformation *message, unsigned long messageCount, unsigned long requestUidStart, unsigned long requestUidEnd, long *startNum, long *endNum)
{
    if (messageCount != 0) {
        unsigned long lastMessageId;
        /* contents of a range are independent of the order of the range endpoints.  (rfc3501 6.4.8) */
        if (requestUidStart <= requestUidEnd) {
            lastMessageId = messageCount - 1;
        } else {
            unsigned long tmp;
            tmp = requestUidEnd;
            requestUidEnd = requestUidStart;
            requestUidStart = tmp;
            lastMessageId = messageCount - 1;
        }

        if (requestUidEnd > message[0].uid) {
            if (requestUidStart < message[lastMessageId].uid) {
                *startNum = FindClosestUid(GREATER_THAN_OR_EQUAL_TO, requestUidStart, message, 0, lastMessageId);
                *endNum = FindClosestUid(LESS_THAN_OR_EQUAL_TO, requestUidEnd,  message, *startNum, lastMessageId);
                return(STATUS_CONTINUE);
            }

            if (requestUidStart == message[lastMessageId].uid) {
                *startNum = lastMessageId;
                *endNum = lastMessageId;
                return(STATUS_CONTINUE);
            }

            return(STATUS_UID_NOT_FOUND);
        }

        if (requestUidEnd == message[0].uid) {
            *startNum = 0;
            *endNum = 0;
            return(STATUS_CONTINUE);
        }
        return(STATUS_UID_NOT_FOUND);
    }
    return(STATUS_UID_NOT_FOUND);
}

__inline static long 
GetMessageUidRange(MessageInformation *message, unsigned long messageCount, char *range, unsigned long *rangeStart, unsigned long *rangeEnd)
{
    char *colonPtr;
    unsigned long start;
    unsigned long end;
    long ccode;

    if (messageCount > 0) {
        /* the mailbox is not empty */
        if ((colonPtr = strchr(range, ':')) != NULL) {
            /* parse the set */
            start = (unsigned long)atol(range);     
            if (start > 0) {
                ;
            } else if (*range == '*') {
                start = message[messageCount - 1].uid;
            } else {
                return(STATUS_INVALID_ARGUMENT);
            }

            if (*(colonPtr + 1) == '*') {
                end = message[messageCount - 1].uid;
                return(UidToSequenceRange(message, messageCount, start, end, rangeStart, rangeEnd));
            }

            end = (unsigned long)atol(colonPtr + 1);
            if (end != 0) {
                return(UidToSequenceRange(message, messageCount, start, end, rangeStart, rangeEnd));
            }
            return(STATUS_INVALID_ARGUMENT);
        }

        /* Just a single number */
        if (*range == '*') {
            /* Special: Last available item */
            *rangeStart = messageCount - 1;
            *rangeEnd = *rangeStart;
            return(STATUS_CONTINUE);
        }

        *rangeStart = (unsigned long)atol(range);

        ccode = UidToSequenceNum(message, messageCount, *rangeStart, rangeStart);
        if (ccode == STATUS_CONTINUE) {
            *rangeEnd = *rangeStart;
            return(STATUS_CONTINUE);
        }
    }
    return(STATUS_UID_NOT_FOUND);  /* nothing found */
}

__inline static long 
GetMessageIdRange(unsigned long messageCount, char *range, unsigned long *rangeStart, unsigned long *rangeEnd)
{
    char *colonPtr;
    unsigned long start;
    unsigned long end;

    if (messageCount > 0) {
        /* the mailbox is not empty */
        if ((colonPtr = strchr(range, ':')) != NULL) {
            /* We've got a set ! */
            start = (unsigned long)atol(range);     
            if (start > 0) {
                ;
            } else if (*range == '*') {
                start = messageCount;
            } else {
                return(STATUS_INVALID_ARGUMENT);
            }

            if (*(colonPtr + 1) == '*') {
                end = messageCount;
            } else {
                end = (unsigned long)atol(colonPtr + 1);
                if (end != 0) {
                    ;
                } else {
                    return(STATUS_INVALID_ARGUMENT);
                }
            }

            /* switch if the larger number is first */
            if (start <= end) {
                ;
            } else {
                unsigned long tmp;

                tmp = start;
                start = end;
                end = tmp;
            }

            start--;
            end--;

            if (end < messageCount) {
                *rangeStart = start;
                *rangeEnd = end;
                return(STATUS_CONTINUE);
            }
            return(STATUS_INVALID_ARGUMENT);
        }

        /* Just a single number */
        if (*range == '*') {
            /* Special: Last available item */
            *rangeStart = messageCount - 1;
            *rangeEnd = *rangeStart;
            return(STATUS_CONTINUE);
        }

        start = (unsigned long)atol(range);

        start--;
        if (start < messageCount) {
            *rangeStart = start;
            *rangeEnd = *rangeStart;
            return(STATUS_CONTINUE);
        }
    }

    return(STATUS_INVALID_ARGUMENT);
}


long 
GetMessageRange(MessageInformation *message, unsigned long messageCount, char **nextRange, unsigned long *rangeStart, unsigned long *rangeEnd, BOOL byUid)
{
    char *currentRange;
    char *commaPtr;
    long ccode;

    currentRange = *nextRange;

    if ((commaPtr = strchr(currentRange, ',')) == NULL) {
        if (byUid) {
            ccode = GetMessageUidRange(message, messageCount, currentRange, rangeStart, rangeEnd);   
        } else {
            ccode = GetMessageIdRange(messageCount, currentRange, rangeStart, rangeEnd);
        }
        *nextRange = NULL;
        return(ccode);
    }

    *commaPtr = '\0';
    if (byUid) {
        ccode = GetMessageUidRange(message, messageCount, currentRange, rangeStart, rangeEnd);   
    } else {
        ccode = GetMessageIdRange(messageCount, currentRange, rangeStart, rangeEnd);
    }
    *commaPtr = ',';
    *nextRange = commaPtr + 1;
    return(ccode);
}

long
TestUidToSequenceRange(MessageInformation *message, unsigned long messageCount, unsigned long requestUidStart, unsigned long requestUidEnd, long *startNum, long *endNum)
{
    return(UidToSequenceRange(message, messageCount, requestUidStart, requestUidEnd, startNum, endNum));
}
