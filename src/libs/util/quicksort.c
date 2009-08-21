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
#include <memmgr.h>
#include <bongoutil.h>

static BOOL
QuickSortCaseInSensitive(char **stringList, unsigned long *sortedList, long beginPos, long endPos)
{
    long relation;
    long setSize;
    unsigned long tmpValue;

    setSize = endPos - beginPos;
    if (setSize > 1) {
        long lowPoint;
        long highPoint;
        unsigned long midPoint;
        unsigned long midValue;
        char *midString;

        midPoint = ((setSize) >> 1) + beginPos;
        midValue = sortedList[midPoint];
        midString = stringList[midValue];

        /* move midValue out of the way */
        tmpValue = sortedList[endPos];
        sortedList[endPos] = midValue;
        sortedList[midPoint] = tmpValue;
    
        lowPoint = beginPos;
        highPoint = endPos - 1;


        do {
            /* start at the beginning and search forward to find first string bigger than midValue */
            do {
                if (XplStrCaseCmp(stringList[sortedList[lowPoint]], midString) < 0) {
                    lowPoint++;
                    if (lowPoint < highPoint) {
                        continue;
                    }
                    
                }
                break;
            } while (TRUE);

            /* start at the end and search backward to find first string less than midValue */    
            do {
                if (XplStrCaseCmp(stringList[sortedList[highPoint]], midString) > 0) {
                    highPoint--;
                    if (highPoint > lowPoint) {
                        continue;
                    }
                }
                break;
            } while (TRUE);
            
            if (highPoint > lowPoint) {
                tmpValue = sortedList[lowPoint];
                sortedList[lowPoint] = sortedList[highPoint];
                sortedList[highPoint] = tmpValue;

                lowPoint++;
                highPoint--;
                if (highPoint > lowPoint) {
                    continue;
                }

                if (highPoint == lowPoint) {
                    if (XplStrCaseCmp(stringList[sortedList[highPoint]], midString) < 0) {
                        highPoint++;
                    }
                    break;
                }

                highPoint++;
                break;
            }

            if (XplStrCaseCmp(stringList[sortedList[highPoint]], midString) < 0) {
                highPoint++;
            }
            break;
        } while (TRUE); 

        /* move midValue to the middle */
        tmpValue = sortedList[highPoint];
        sortedList[highPoint] = midValue;
        sortedList[endPos] = tmpValue;

        if ((highPoint - 1) > beginPos) {
            if (QuickSortCaseInSensitive(stringList, sortedList, beginPos, highPoint - 1) == TRUE) {
                ;
            } else {
                return(FALSE);
            }
        }

        if (endPos > (highPoint + 1)) {
            if (QuickSortCaseInSensitive(stringList, sortedList, highPoint + 1, endPos)) {
                return(TRUE);
            }
            return(FALSE);
        }
        return(TRUE);
    }

    if (setSize == 1) {
        relation = XplStrCaseCmp(stringList[sortedList[beginPos]], stringList[sortedList[endPos]]);
        if (relation < 0) {              
            return(TRUE);
        }
        
        if (relation > 0) {
            tmpValue = sortedList[beginPos];
            sortedList[beginPos] = sortedList[endPos];
            sortedList[endPos] = tmpValue;
            return(TRUE);
        }

        /* duplicate string */
        return(FALSE);
    }

    if (setSize > -1) {
        return(TRUE);
    }

    return(FALSE);
}

static BOOL
QuickSortCaseSensitive(char **stringList, unsigned long *sortedList, long beginPos, long endPos)
{
    long relation;
    long setSize;
    unsigned long tmpValue;

    setSize = endPos - beginPos;
    if (setSize > 1) {
        long lowPoint;
        long highPoint;
        unsigned long midPoint;
        unsigned long midValue;
        char *midString;

        midPoint = ((setSize) >> 1) + beginPos;
        midValue = sortedList[midPoint];
        midString = stringList[midValue];

        /* move midValue out of the way */
        tmpValue = sortedList[endPos];
        sortedList[endPos] = midValue;
        sortedList[midPoint] = tmpValue;
    
        lowPoint = beginPos;
        highPoint = endPos - 1;


        do {
            /* start at the beginning and search forward to find first string bigger than midValue */
            do {
                if (strcmp(stringList[sortedList[lowPoint]], midString) < 0) {
                    lowPoint++;
                    if (lowPoint < highPoint) {
                        continue;
                    }

                }
                break;
            } while (TRUE);

            /* start at the end and search backward to find first string less than midValue */    
            do {
                if (strcmp(stringList[sortedList[highPoint]], midString) > 0) {
                    highPoint--;
                    if (highPoint > lowPoint) {
                        continue;
                    }
                }
                break;
            } while (TRUE);
            
            if (highPoint > lowPoint) {
                tmpValue = sortedList[lowPoint];
                sortedList[lowPoint] = sortedList[highPoint];
                sortedList[highPoint] = tmpValue;

                lowPoint++;
                highPoint--;
                if (highPoint > lowPoint) {
                    continue;
                }

                if (highPoint == lowPoint) {
                    if (strcmp(stringList[sortedList[highPoint]], midString) < 0) {
                        highPoint++;
                    }
                    break;
                }

                highPoint++;
                break;
            }

            if (strcmp(stringList[sortedList[highPoint]], midString) < 0) {
                highPoint++;
            }
            break;
        } while (TRUE);

        /* move midValue to the middle */
        tmpValue = sortedList[highPoint];
        sortedList[highPoint] = midValue;
        sortedList[endPos] = tmpValue;

        if ((highPoint - 1) > beginPos) {
            if (QuickSortCaseSensitive(stringList, sortedList, beginPos, highPoint - 1) == TRUE) {
                ;
            } else {
                return(FALSE);
            }
        }

        if (endPos > (highPoint + 1)) {
            if (QuickSortCaseSensitive(stringList, sortedList, highPoint + 1, endPos)) {
                return(TRUE);
            }
            return(FALSE);
        }
        return(TRUE);
    }

    if (setSize == 1) {
        relation = strcmp(stringList[sortedList[beginPos]], stringList[sortedList[endPos]]);
        if (relation < 0) {
            return(TRUE);
        }
        
        if (relation > 0) {
            tmpValue = sortedList[beginPos];
            sortedList[beginPos] = sortedList[endPos];
            sortedList[endPos] = tmpValue;
            return(TRUE);
        }

        /* duplicate string */
        return(FALSE);
    }

    if (setSize > -1) {
        return(TRUE);
    }

    return(FALSE);
}

unsigned long *
QuickSortIndexCreate(char **stringList, unsigned long count, BOOL caseInsensitive)
{
    unsigned long *sortedList;
    unsigned long i;

    sortedList = MemMalloc(sizeof(unsigned long) * count);
    if (sortedList) {
        for (i = 0; i < count; i++) {
            sortedList[i] = i;
        }

        if (caseInsensitive) {
            if (QuickSortCaseInSensitive(stringList, sortedList, 0, count - 1)) {
                return(sortedList);
            }
        } else {
            if (QuickSortCaseSensitive(stringList, sortedList, 0, count - 1)) {
                return(sortedList);
            }
        }

        MemFree(sortedList);
    }
    return(NULL);
}

void
QuickSortIndexFree(unsigned long *index)
{
    MemFree(index);
}
