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

int KeywordCompareCaseSensitive(const void *param1, const void *param2);
int KeywordCompareCaseInSensitive(const void *param1, const void *param2);

static unsigned long
AddRow(BongoKeywordIndex *index, unsigned long rangeStart, unsigned long rangeCount, unsigned long identical)
{
    BongoKeywordTable *tmpTable;
    unsigned char lastChar;
    unsigned char currentChar;
    unsigned long stringsProcessed;
    unsigned long processed;
    unsigned long i;
    unsigned long subRangeStart;
    unsigned long subRangeCount;
    unsigned long currentRow;

    stringsProcessed = 0;

    /* this function adds one row that it writes to */
    currentRow = index->tableRows;

    tmpTable = (BongoKeywordTable *)MemRealloc(index->table, sizeof(BongoKeywordTable) * (index->tableRows + 1));
    if (tmpTable) {
        /* initialize new row */
        index->table = tmpTable;
        memset(index->table + index->tableRows, 0, sizeof(BongoKeywordTable));
        index->tableRows++;
        
        lastChar = (unsigned char)(index->caseInsensitive ? tolower(index->keywords[rangeStart].string[identical]) : index->keywords[rangeStart].string[identical]);
        subRangeStart = rangeStart;
        subRangeCount = 1;

        for (i = (rangeStart + 1); i < (rangeStart + rangeCount); i++) {
            currentChar = index->caseInsensitive ? tolower(index->keywords[i].string[identical]) : index->keywords[i].string[identical];
            if (currentChar == lastChar) {
                subRangeCount++;
                continue;
            }

            /* we have a boundry */
            if (subRangeCount > 1) {
                index->table[currentRow][lastChar] = index->tableRows;
                processed = AddRow(index, subRangeStart, subRangeCount, identical + 1);
                if (processed == subRangeCount) {
                    stringsProcessed += subRangeCount;
                    subRangeStart += subRangeCount;
                    subRangeCount = 1;
                    lastChar = currentChar;
                    continue;
                }
                return(stringsProcessed);
            }

            index->table[currentRow][lastChar] = BONGO_KEYWORD_HIGH_BIT_MASK | subRangeStart;
            stringsProcessed++;
            subRangeStart++;
            subRangeCount = 1;
            lastChar = currentChar;
        }

        /* we reached the end of the list which constitutes a boundry */
        if (subRangeCount > 1) {
            index->table[currentRow][lastChar] = index->tableRows;
            stringsProcessed += AddRow(index, subRangeStart, subRangeCount, identical + 1);
            return(stringsProcessed);
        }

        index->table[currentRow][lastChar] = BONGO_KEYWORD_HIGH_BIT_MASK | subRangeStart;
        stringsProcessed++;
        return(stringsProcessed);
    }
    return(0);
}

int
KeywordCompareCaseSensitive(const void *param1, const void *param2)
{
    BongoKeyword *word1 = (BongoKeyword *)param1;
    BongoKeyword *word2 = (BongoKeyword *)param2;

    return(strcmp(word1->string, word2->string));
}

int
KeywordCompareCaseInSensitive(const void *param1, const void *param2)
{
    BongoKeyword *word1 = (BongoKeyword *)param1;
    BongoKeyword *word2 = (BongoKeyword *)param2;

    return(XplStrCaseCmp(word1->string, word2->string));
}

BongoKeywordIndex *
BongoKeywordIndexCreate(char **strings, unsigned long count, BOOL caseInsensitive)
{
    unsigned long i;
    BongoKeywordIndex *index;
    BongoKeyword *keyword;

    index = MemMalloc(sizeof(BongoKeywordIndex));
    if (index) {
        index->caseInsensitive = caseInsensitive;

        keyword = MemMalloc(count * sizeof(BongoKeyword));
        if (keyword) {
            for (i = 0; i < count; i++) {
                keyword[i].string = strings[i];
                keyword[i].len = strlen(strings[i]);
                keyword[i].id = i;
            }

            if (caseInsensitive) {
                qsort(keyword, count, sizeof(BongoKeyword), KeywordCompareCaseInSensitive);
            } else {
                qsort(keyword, count, sizeof(BongoKeyword), KeywordCompareCaseSensitive);
            }

            index->keywords = keyword;
            index->keywordCount = count;
            index->table = NULL;
            index->tableRows = 0;

            if (AddRow(index, 0, count, 0) == count) {
                return(index);
            }

            if (index->table) {
                MemFree(index->table);
            }
            MemFree(keyword);
        }
        MemFree(index);
    }
    return(NULL);
}

__inline static long
FindMatch(BongoKeywordIndex *keywordIndex, char *searchString, BOOL beginsWith)
{
    BongoKeyword *keyword;
    unsigned long row = 0;
    unsigned long nextRow;
    unsigned char *string;
    BongoKeywordTable *table;

    table = keywordIndex->table;
    string = (unsigned char *)searchString;

    if (keywordIndex->caseInsensitive) {
        do {
            nextRow = table[row][tolower(*string)];
             if (nextRow) {
                 if (!(nextRow & BONGO_KEYWORD_HIGH_BIT_MASK)) {
                    row = nextRow;
                    string++;
                    continue;
                }
                
                keyword = &(keywordIndex->keywords[nextRow & ~BONGO_KEYWORD_HIGH_BIT_MASK]);
                if (beginsWith) {
                    if (XplStrNCaseCmp(searchString, keyword->string, keyword->len) == 0) {
                        return(keyword->id);
                    }  
                } else {
                    if (XplStrCaseCmp(searchString, keyword->string) == 0) {
                        return(keyword->id);
                    }  
                }
                break;
            }

            if (beginsWith) {
                nextRow = table[row][0];

                if (nextRow) {
                    if ((nextRow & BONGO_KEYWORD_HIGH_BIT_MASK)) {
                        keyword = &(keywordIndex->keywords[nextRow & ~BONGO_KEYWORD_HIGH_BIT_MASK]);
                        return(keyword->id);
                    }
                }
            }
            break;
        } while (TRUE);
        return(-1);
    }

    /* case sensitive search */
    do {
        nextRow = table[row][*string];
        
        if (nextRow) {
            if (!(nextRow & BONGO_KEYWORD_HIGH_BIT_MASK)) {
                row = nextRow;
                string++;
                continue;
            }
            
            keyword = &(keywordIndex->keywords[nextRow & ~BONGO_KEYWORD_HIGH_BIT_MASK]);
            if (beginsWith) {
                if (strncmp(searchString, keyword->string, keyword->len) == 0) {
                    return(keyword->id);
                }  
            } else {
                if (strcmp(searchString, keyword->string) == 0) {
                    return(keyword->id);
                }  
            }
            break;
        }

        if (beginsWith) {
            nextRow = table[row][0];

            if (nextRow) {
                if ((nextRow & BONGO_KEYWORD_HIGH_BIT_MASK)) {
                    keyword = &(keywordIndex->keywords[nextRow & ~BONGO_KEYWORD_HIGH_BIT_MASK]);
                    return(keyword->id);
                }
            }
        }
        break;
    } while (TRUE);
    return(-1);
}

long
BongoKeywordFind(BongoKeywordIndex *keywordIndex, unsigned char *searchString)
{
    return(FindMatch(keywordIndex, searchString, FALSE));    
}

long
BongoKeywordBegins(BongoKeywordIndex *keywordIndex, unsigned char *searchString)
{
    return(FindMatch(keywordIndex, searchString, TRUE));    
}

void
BongoKeywordIndexFree(BongoKeywordIndex *Index)
{
    MemFree(Index->table);
    MemFree(Index->keywords);
    MemFree(Index);
}
