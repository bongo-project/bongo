/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2001-2006 Novell, Inc. All Rights Reserved.
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

#ifndef _BONGOUTIL_H
#define _BONGOUTIL_H

#include <xpl.h>
#include <stddef.h>
#include <stdarg.h>

XPL_BEGIN_C_LINKAGE

#define	NMAP_HASH_SIZE 128
#define NMAP_CRED_STORE_SIZE 4096
#define NMAP_CRED_DIGEST_SIZE 16
#define NMAP_CRED_CHUNK_SIZE ((NMAP_CRED_STORE_SIZE * NMAP_CRED_DIGEST_SIZE) / NMAP_HASH_SIZE)

#ifdef DEBUG
#define BongoReturnIfFail(cond)                     \
    {                                           \
        if (XPL_UNLIKELY(!(cond))) {            \
            printf("WARNING: assert failed: %s\n", #cond);      \
                                                                \
                return;                          \
        }                                        \
    }                                            \

#define BongoReturnValIfFail(cond, val)                \
    {  \
        if (XPL_UNLIKELY(!(cond))) {            \
            printf("WARNING: assert failed: %s\n", #cond);     \
                                                                \
                return val;                          \
        }                                        \
    }     
#else
#define BongoReturnIfFail(cond)
#define BongoReturnValIfFail(cond, val)
#endif

#ifndef offsetof
#define offsetof(s, m) ((size_t) &((s *)0)->m)
#endif

uint64_t HexToUInt64(char *hexString, char **endp);
                                                                                                                                                                            
enum CommandColors {
    RedCommand = 0,
    BlackCommand,
                                                                                                                                                                            
    MaxCommandColors
};
                                                                                                                                                                            
typedef int (* CommandHandler)(void *param);

typedef struct _ProtocolCommand {
    unsigned char *name;
    unsigned char *help;
                                                                                                                                                                            
    int length;
                                                                                                                                                                            
    CommandHandler handler;
                                                                                                                                                                            
    struct _ProtocolCommand *parent;
    struct _ProtocolCommand *left;
    struct _ProtocolCommand *right;
                                                                                                                                                                            
    enum CommandColors color;
} ProtocolCommand;
                                                                                                                                                                            
typedef struct _ProtocolCommandTree {
    ProtocolCommand *sentinel;
    ProtocolCommand *root;
                                                                                                                                                                            
    XplAtomic nodes;
                                                                                                                                                                            
    ProtocolCommand proxy;
} ProtocolCommandTree;

extern const unsigned char *Base64Chars;

/***** Begin BongoKeyword *****/
#define BONGO_KEYWORD_VALUES_PER_BYTE 256
#define BONGO_KEYWORD_BITS_PER_BYTE 8
#define BONGO_KEYWORD_BITS_PER_WORD (sizeof(unsigned long) * BONGO_KEYWORD_BITS_PER_BYTE)
#define BONGO_KEYWORD_FULL_MASK (unsigned long)(-1)
#define BONGO_KEYWORD_HIGH_BIT_MASK ((unsigned long)(1) << (BONGO_KEYWORD_BITS_PER_WORD - 1))
/* BONGO_KEYWORD_BBPW (bits required to store the number of bits in a word) */
/* could be calculated as */
/* (int BBPW) */
/* after calling frexp(BITS_PER_WORD, &BBPW) */
/* but it would end up getting calculated at run time */
#define BONGO_KEYWORD_BBPW ((BONGO_KEYWORD_BITS_PER_WORD < 33) ? 5 : (BONGO_KEYWORD_BITS_PER_WORD < 65) ? 6 : (BONGO_KEYWORD_BITS_PER_WORD < 129) ? 7 : (BONGO_KEYWORD_BITS_PER_WORD < 257) ? 8 : 9)
/* BONGO_KEYWORD_BTIM is the number of bits in the BONGO_KEYWORD_TABLE_INDEX_MASK */
#define BONGO_KEYWORD_BTIM ((BONGO_KEYWORD_BITS_PER_WORD - 1 - BONGO_KEYWORD_BBPW) >> 1)
#define BONGO_KEYWORD_TABLE_INDEX_MASK (BONGO_KEYWORD_FULL_MASK >> (BONGO_KEYWORD_BITS_PER_WORD - BONGO_KEYWORD_BTIM))
#define BONGO_KEYWORD_STRING_INDEX_MASK  (~(BONGO_KEYWORD_HIGH_BIT_MASK | BONGO_KEYWORD_TABLE_INDEX_MASK))

typedef unsigned long BongoKeywordTable[BONGO_KEYWORD_VALUES_PER_BYTE];

typedef struct {
    char *string;
    unsigned long len;
    unsigned long id;
} BongoKeyword;

typedef struct {
    BongoKeywordTable *table;
    unsigned long tableRows;
    BongoKeyword *keywords;
    unsigned long keywordCount;
    BOOL caseInsensitive;
} BongoKeywordIndex;

BongoKeywordIndex *BongoKeywordIndexCreate(char **strings, unsigned long count, BOOL makeCaseInsensitive);
void BongoKeywordIndexFree(BongoKeywordIndex *Index);
long BongoKeywordFind(BongoKeywordIndex *keywordTable, unsigned char *searchString);
long BongoKeywordBegins(BongoKeywordIndex *keywordIndex, unsigned char *searchString);
BongoKeywordIndex *BongoKeywordIndexCreateProtocolCommands(ProtocolCommand *ProtocolCommands);

#define BongoKeywordIndexCreateFromTable(INDEX, TABLE, COLUMN, CASEINSENSITIVE) \
{ \
    char **strings; \
    unsigned long count; \
    unsigned long row; \
    count = 0; \
    while(TABLE[count]COLUMN != NULL) { \
        count++; \
    } \
    strings = MemMalloc(sizeof(unsigned char *) * (count + 1)); \
    if (strings) { \
        for (row = 0; row < count; row++) { \
            strings[row] = TABLE[row]COLUMN; \
        } \
        strings[count] = NULL; \
        INDEX = BongoKeywordIndexCreate(strings, count, CASEINSENSITIVE); \
        MemFree(strings); \
    } else { \
        INDEX = NULL; \
    } \
}

/***** End BongoKeyword *****/

unsigned long *QuickSortIndexCreate(char **stringList, unsigned long count, BOOL caseInsensitive);
void QuickSortIndexFree(unsigned long *index);

void LoadProtocolCommandTree(ProtocolCommandTree *tree, ProtocolCommand *commands);
ProtocolCommand *ProtocolCommandTreeSearch(ProtocolCommandTree *tree, const unsigned char *command);

BOOL QuickNCmp(unsigned char *str1, unsigned char *str2, int len);
BOOL QuickCmp(unsigned char *str1, unsigned char *str2);

#define BongoStrNCpy(dest, src, len) {strncpy((dest), (src), (len)); (dest)[(len) - 1] = '\0';}

unsigned char *DecodeBase64(unsigned char *EncodedString);
unsigned char *EncodeBase64(const unsigned char *UnencodedString);

BOOL HashCredential(unsigned char *Credential, unsigned char *Hash);

/* String functions */

int BongoStringSplit(char *str, char sep, char **ret, int maxstrings);
int BongoMemStringSplit(const char *str, char sep, char **ret, int maxstrings);
void BongoMemStringSplitFree(char **ret, int num);

char *BongoStringJoin(char **strings, int num, char sep);

char *BongoStrTrim(char *str);
char *BongoStrTrimDup(const char *str);

void BongoStrToLower(char *str);
char *BongoMemStrToLower(const char *str);

void BongoStrToUpper(char *str);
char *BongoMemStrToUpper(const char *str);

char *Collapse(char *ch);
int CommandSplit(char *str, char **ret, int retsize);

char *BongoStrCaseStr(char *haystack, char *needle);


/** hashtable.c **/

struct BongoHashtableEntry {
    uint32_t hashcode;
    void *key;
    void *value;
    struct BongoHashtableEntry *next;
};

typedef uint32_t (* HashFunction) (const void *key);
/* must return 0 iff keya == keyb) */
typedef int (* CompareFunction) (const void *keya, const void *keyb); 

typedef void (* DeleteFunction) (void *obj);

typedef void (* HashtableForeachFunc) (void *key, void *value, void *data);

typedef struct {
    int itemcount;
    int bucketcount;
    HashFunction hashfunc;
    CompareFunction compfunc;
    DeleteFunction keydeletefunc;
    DeleteFunction valuedeletefunc;
    
    struct BongoHashtableEntry *buckets[0];
} BongoHashtable;

typedef struct {
    /* public */
    void *key;
    void *value;
    
    /* private */
    struct BongoHashtableEntry *entry;
    int bucket;
} BongoHashtableIter;

BongoHashtable *BongoHashtableCreate(int buckets, 
                                   HashFunction hashfunc, 
                                   CompareFunction compfunc);
BongoHashtable *BongoHashtableCreateFull(int buckets, 
                                       HashFunction hashfunc,
                                       CompareFunction compfunc,
                                       DeleteFunction keydeletefunc,
                                       DeleteFunction valuedeletefunc);

void *BongoHashtableGet(const BongoHashtable *table, const void *key);

int BongoHashtablePut(BongoHashtable *table, void *key, void *value);
int BongoHashtablePutNoReplace(BongoHashtable *table, void *key, void *value);

void BongoHashtableForeach(BongoHashtable *table, HashtableForeachFunc func, void *data);

BOOL BongoHashtableIterFirst(BongoHashtable *table, BongoHashtableIter *iter);
BOOL BongoHashtableIterNext(BongoHashtable *table, BongoHashtableIter *iter);

void *BongoHashtableRemove(BongoHashtable *table, const void *key);
void *BongoHashtableRemoveFull(BongoHashtable *table, const void *key, 
                              BOOL deleteKey, BOOL deleteValue);
void *BongoHashtableRemoveKeyValue(BongoHashtable *table, const void *key, void *value,
                                  BOOL deleteKey, BOOL deleteValue);
int BongoHashtableSize(BongoHashtable *table);
void BongoHashtableClear(BongoHashtable *table);
void BongoHashtableClearFull(BongoHashtable *table, BOOL deleteKeys, BOOL deleteValues);
void BongoHashtableDelete(BongoHashtable *table);

#define BongoHashTableCount(_tableptr) ((_tableptr)->itemcount)

#define BongoCreateStringHashTable(buckets) \
BongoHashtableCreate(buckets, (HashFunction)BongoStringHash, (CompareFunction)strcmp)

/* handy string hasher */
uint32_t BongoStringHash (char *s);

/* macro version of same */
#define BONGO_STRING_HASH(_strptr, _int32result)  \
        {                                        \
            uint32_t b = 1;                      \
            uint32_t h = 0;                      \
            char *c;                             \
            for (c = (_strptr); *c; c++) {       \
                h += b * tolower(*c);            \
                b = (b << 5) - b;                \
            }                                    \
            _int32result = h;                    \
        }

/** end hashtable.c **/

/** begin utf7mod.c **/

#define UTF7_STATUS_SUCCESS 0
#define UTF7_STATUS_INVALID_UTF8 -1
#define UTF7_STATUS_INVALID_UTF7 -2
#define UTF7_STATUS_OUTPUT_BUFFER_TOO_SMALL -3
#define UFF7_STATUS_MEMORY_ERROR -4

int ModifiedUtf7ToUtf8(char *utf7Start, unsigned long utf7Len, char *utf8Buffer, unsigned long utf8BufferLen);
int Utf8ToModifiedUtf7(char *utf8Start, unsigned long utf8Len, char *utf7Buffer, unsigned long utf7BufferLen);
int GetModifiedUtf7FromUtf8(char *utf8Start, unsigned long utf8Len, char **result);
int GetUtf8FromModifiedUtf7(char *utf7Start, unsigned long utf7Len, char **result);
int ValidateModifiedUtf7(char *utf7String);
int ValidateUtf8(char *utf8String);
int ValidateUtf7Utf8Pair(char *utf7String, size_t utf7StringLen, char *utf8String, size_t utf8StringLen);

/** end utf7mod.c **/


/** begin lists.c **/

typedef struct _BongoList BongoList;
typedef struct _BongoSList BongoSList;

struct _BongoList {
    void *data;
    BongoList *next;
    BongoList *prev;
};

struct _BongoSList {
    void *data;
    BongoSList *next;
};

BongoList *BongoListCopy(BongoList *list);
BongoList *BongoListAppend(BongoList *list, void *data);
BongoList *BongoListAppendList(BongoList *list, BongoList *list2);
BongoList *BongoListPrepend(BongoList *list, void *data);
BongoList *BongoListReverse(BongoList *list);
void BongoListFree(BongoList *list);
void BongoListFreeDeep(BongoList *list);
int BongoListLength(BongoList *list);
BongoList *BongoListDelete(BongoList *list, void *data, BOOL deep);

BongoSList *BongoSListCopy(BongoSList *slist);
BongoSList *BongoSListAppend(BongoSList *slist, void *data);
BongoSList *BongoSListAppendSList(BongoSList *slist, BongoSList *slist2);
BongoSList *BongoSListPrepend(BongoSList *slist, void *data);
BongoSList *BongoSListReverse(BongoSList *slist);
void BongoSListFree(BongoSList *slist);
void BongoSListFreeDeep(BongoSList *slist);
int BongoSListLength(BongoSList *slist);
BongoSList *BongoSListDelete(BongoSList *list, void *data, BOOL deep);

/** end lists.c **/

/** begin array.c **/

typedef struct _BongoArray BongoArray;

struct _BongoArray {
    void *data;
    unsigned int len;
    unsigned int size;
    unsigned int elemSize;
};

typedef int (*ArrayCompareFunc)(const void *, const void *);

int BongoArrayInit(BongoArray *array, 
                  unsigned int elementSize, unsigned int numElements);
void *BongoArrayDestroy(BongoArray *array, BOOL freeSegment);

BongoArray *BongoArrayNew(unsigned int elementSize, unsigned int numElements);
void *BongoArrayFree(BongoArray *array, BOOL freeSegment);

#define BongoArrayIndex(a,t,i) (((t*)(a)->data)[(i)])
#define BongoArrayAppendValue(array,value) BongoArrayAppendValues((array), &(value), 1);

void BongoArrayAppendValues(BongoArray *array, const void *data, unsigned int len);
void BongoArrayRemove(BongoArray *array, unsigned int index);

void BongoArraySetLength(BongoArray *array, unsigned int size);

void BongoArraySort(BongoArray *array, ArrayCompareFunc compare);
int BongoArrayFindSorted(BongoArray *array, void *needle, ArrayCompareFunc compare);
int BongoArrayFindUnsorted(BongoArray *array, void *needle, ArrayCompareFunc compare);


#define BongoArrayCount(arrayptr) ((arrayptr)->len)

/** end array.c **/

/** begin stringbuilder.c **/

typedef struct _BongoStringBuilder BongoStringBuilder;

struct _BongoStringBuilder {
    char *value;
    unsigned int len;
    unsigned int size;
};

BongoStringBuilder *BongoStringBuilderNew(void);
BongoStringBuilder *BongoStringBuilderNewSized(int size);

int BongoStringBuilderInit(BongoStringBuilder *sb);
int BongoStringBuilderInitSized(BongoStringBuilder *sb, int size);

void BongoStringBuilderDestroy(BongoStringBuilder *sb);

char *BongoStringBuilderFree(BongoStringBuilder *sb, BOOL freeSegment);

void BongoStringBuilderAppend(BongoStringBuilder *sb, const char *str);
void BongoStringBuilderAppendN(BongoStringBuilder *sb, const char *str, int len);
void BongoStringBuilderAppendF(BongoStringBuilder *sb, const char *format, ...) XPL_PRINTF(2, 3);

void BongoStringBuilderAppendVF(BongoStringBuilder *sb, const char *format, va_list args);
void BongoStringBuilderAppendChar(BongoStringBuilder *sb, char c);

/** end stringbuilder.c **/

/** begin fileio.c **/

int BongoFileCopyBytes(FILE *source, FILE *dest, size_t count);
int BongoFileAppendBytes(FILE *source, FILE *dest, size_t start, size_t end);

/** end fileio.c **/

XPL_END_C_LINKAGE

#endif /* _BONGOUTIL_H */
