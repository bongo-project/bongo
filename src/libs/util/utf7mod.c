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


/* Modified Base64 encoding/decoding tables; used in mUTF-7  */
static unsigned char MBASE64[] = {"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+,"};
static unsigned char MBASE64_R[256] = {
    0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
    0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
    0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x3E, 0x3F, 0xFF, 0xFF, 0xFF,
    0x34, 0x35, 0x36, 0x37, 0x38, 0x39, 0x3A, 0x3B, 0x3C, 0x3D, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
    0xFF, 0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E,
    0x0F, 0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
    0xFF, 0x1A, 0x1B, 0x1C, 0x1D, 0x1E, 0x1F, 0x20, 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28,
    0x29, 0x2A, 0x2B, 0x2C, 0x2D, 0x2E, 0x2F, 0x30, 0x31, 0x32, 0x33, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
    0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
    0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
    0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
    0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
    0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
    0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
    0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
    0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
};

__inline static void
ModifiedBase64Encode3BytesNoCheck(unsigned char octet[3], unsigned long *octetCount, unsigned char **out)
{
    if (*octetCount < 3) {
        return;
    }

    **out = MBASE64[((octet[0] & 0xFC) >> 2)];
    (*out)++;

    **out = MBASE64[((octet[0] & 0x03) << 4) | ((octet[1] & 0xF0) >> 4)];
    (*out)++;

    **out = MBASE64[((octet[1] & 0x0F) << 2) | ((octet[2] & 0xC0) >> 6)];
    (*out)++;

    **out = MBASE64[((octet[2] & 0x3F) << 0)];
    (*out)++;

    (*octetCount) -= 3;
    memmove(octet, octet + 3, *octetCount);
    return;
}

__inline static BOOL
ModifiedBase64Encode3Bytes(unsigned char octet[3], unsigned long *octetCount, unsigned char **out, unsigned char *outEnd)
{
    if (*octetCount < 3) {
        return(TRUE);
    }

    if ((*out + 3) < outEnd) {

        **out = MBASE64[((octet[0] & 0xFC) >> 2)];
        (*out)++;

        **out = MBASE64[((octet[0] & 0x03) << 4) | ((octet[1] & 0xF0) >> 4)];
        (*out)++;

        **out = MBASE64[((octet[1] & 0x0F) << 2) | ((octet[2] & 0xC0) >> 6)];
        (*out)++;

        **out = MBASE64[((octet[2] & 0x3F) << 0)];
        (*out)++;

        (*octetCount) -= 3;
        memmove(octet, octet + 3, *octetCount);
        return(TRUE);
    }
   return(FALSE);
}

__inline static void
ModifiedBase64EncodeLeftoversNoCheck(unsigned char octet[3], unsigned long *octetCount, unsigned char **out)
{
    if (*octetCount == 0) {
        return;
    }

    if (*octetCount == 1) {
        octet[1] = 0;

        **out = MBASE64[((octet[0] & 0xFC) >> 2)];
        (*out)++;

        **out = MBASE64[((octet[0] & 0x03) << 4) | ((octet[1] & 0xF0) >> 4)];
        (*out)++;

        (*octetCount) = 0;
        return;
    }

    if (*octetCount == 2) {

        octet[2] = 0;

        **out = MBASE64[((octet[0] & 0xFC) >> 2)];
        (*out)++;

        **out = MBASE64[((octet[0] & 0x03) << 4) | ((octet[1] & 0xF0) >> 4)];
        (*out)++;

        **out = MBASE64[((octet[1] & 0x0F) << 2) | ((octet[2] & 0xC0) >> 6)];
        (*out)++;

        (*octetCount) = 0;
        return;
    }
}

__inline static BOOL
ModifiedBase64EncodeLeftovers(unsigned char octet[3], unsigned long *octetCount, unsigned char **out, unsigned char *outEnd)
{
    if (*octetCount == 0) {
        return(TRUE);
    }

    if (*octetCount == 1) {
        if ((*out + 1) < outEnd) {
            octet[1] = 0;

            **out = MBASE64[((octet[0] & 0xFC) >> 2)];
            (*out)++;

            **out = MBASE64[((octet[0] & 0x03) << 4) | ((octet[1] & 0xF0) >> 4)];
            (*out)++;

            (*octetCount) = 0;
            return(TRUE);
        }
        return(FALSE);
    }

    if (*octetCount == 2) {
        if ((*out + 2) < outEnd) {
            octet[2] = 0;

            **out = MBASE64[((octet[0] & 0xFC) >> 2)];
            (*out)++;

            **out = MBASE64[((octet[0] & 0x03) << 4) | ((octet[1] & 0xF0) >> 4)];
            (*out)++;

            **out = MBASE64[((octet[1] & 0x0F) << 2) | ((octet[2] & 0xC0) >> 6)];
            (*out)++;

            (*octetCount) = 0;
            return(TRUE);
        }
        return(FALSE);
    }

    return(FALSE);
}


__inline static BOOL
Utf8EncodeBmpChar(unicode unichar, unsigned char **dest, unsigned char *destEnd)
{
    if (unichar < 0x0080) {                     /* 0000-007F */
        if (*dest < destEnd) {
            **dest = (char)(unichar);
            (*dest)++;
            return(TRUE);
        }
        return(FALSE);
    }

    if (unichar < 0x0800) {                     /* 0080-07FF */
        if ((*dest + 1) < destEnd) {
            **dest = (char)(0xC0 | ((unichar & 0x07C0) >> 6));
            (*dest)++;
            **dest = (char)(0x80 | (unichar & 0x003F));
            (*dest)++;
            return(TRUE);
        }
        return(FALSE);
    }

    if ((*dest + 2) < destEnd) {               /* 0800-FFFF */
        **dest = (char)(0xE0 | ((unichar & 0xF000) >> 12));
        (*dest)++;
        **dest = (char)(0x80 | ((unichar & 0x0FC0) >> 6));
        (*dest)++;
        **dest = (char)(0x80 | (unichar & 0x003F));
        (*dest)++;
        return(TRUE);    
    }
    return(FALSE);
}

__inline static void
Utf8EncodeBmpCharNoCheck(unicode unichar, unsigned char **dest)
{
    if (unichar < 0x0080) {                     /* 0000-007F */
        **dest = (char)(unichar);
        (*dest)++;
        return;
    }

    if (unichar < 0x0800) {                     /* 0080-07FF */
        **dest = (char)(0xC0 | ((unichar & 0x07C0) >> 6));
        (*dest)++;
        **dest = (char)(0x80 | (unichar & 0x003F));
        (*dest)++;
        return;
    }
                                                /* 0800-FFFF */
    **dest = (char)(0xE0 | ((unichar & 0xF000) >> 12));
    (*dest)++;
    **dest = (char)(0x80 | ((unichar & 0x0FC0) >> 6));
    (*dest)++;
    **dest = (char)(0x80 | (unichar & 0x003F));
    (*dest)++;
    return;    
}


__inline static BOOL
Utf8EncodeSupplementaryChar(unicode unicharHigh, unicode unicharLow, unsigned char **dest, unsigned char *destEnd)
{
    unicode plane;

    if ((*dest + 4) < destEnd) {
        plane = (((unicharHigh >> 6) & 0x000F) + 1);

        **dest = 0xF0 | (plane >> 2);
        (*dest)++;

        **dest = 0x80 | ((plane & 0x0003) << 4) | ((unicharHigh >> 2) & 0x000F);
        (*dest)++;

        **dest = 0x80 | ((unicharHigh & 0x0003) << 4) | ((unicharLow >> 6) & 0x000F);
        (*dest)++;

        **dest = 0x80 | (unicharLow & 0x003F);
        (*dest)++;
        return(TRUE);
    }

    return (FALSE);
}

__inline static void
Utf8EncodeSupplementaryCharNoCheck(unicode unicharHigh, unicode unicharLow, unsigned char **dest)
{
    unicode plane;

    plane = (((unicharHigh >> 6) & 0x000F) + 1);

    **dest = 0xF0 | (plane >> 2);
    (*dest)++;

    **dest = 0x80 | ((plane & 0x0003) << 4) | ((unicharHigh >> 2) & 0x000F);
    (*dest)++;

    **dest = 0x80 | ((unicharHigh & 0x0003) << 4) | ((unicharLow >> 6) & 0x000F);
    (*dest)++;

    **dest = 0x80 | (unicharLow & 0x003F);
    (*dest)++;
    return;
}


__inline static int
Utf8CharToUtf16(const unsigned char *src, const unsigned char *end, unicode *high, unicode *low)
{
    unicode plane;

    if (src[0] < 0x80) {                                        /* 0000-007F: one byte (0xxxxxxx) */
        /* ASCII goes straight through */
        *low = (unicode)src[0];
        return(1);
    }

    if (src[0] < 0xC0) {                                       
        /* Invalid UTF-8 */
        return(UTF7_STATUS_INVALID_UTF8);
    }

    if (src[0] < 0xE0) {
        if (((src + 1) < end) && 
           ((src[1] & 0xC0) == 0x80)) {                         /* 0080-07FF: two bytes (110xxxxx 10xxxxxx) */

            *low = ((((unicode)src[0]) & 0x001F) << 6) |
                   (((unicode)src[1]) & 0x003F);
            return(2);
        }

        return(UTF7_STATUS_INVALID_UTF8);
    }

    if (src[0] < 0xF0) {
        if (((src + 2) < end) && 
           ((src[1] & 0xC0) == 0x80) && 
           ((src[2] & 0xC0) == 0x80)) {                         /* 0800-FFFF: three bytes (1110xxxx 10xxxxxx 10xxxxxx) */

            *low = ((((unicode)src[0]) & 0x000F) << 12) |
                   ((((unicode)src[1]) & 0x003F) << 6) |
                   (((unicode)src[2]) & 0x003F);
            return(3);
        }

        return(UTF7_STATUS_INVALID_UTF8);
    }
        
    if (src[0] < 0xF8) {
        if (((src + 3) < end) &&
           ((src[1] & 0xC0) == 0x80) && 
           ((src[2] & 0xC0) == 0x80) && 
           ((src[3] & 0xC0) == 0x80)) {                         /* 10000-10FFFF: four bytes (11110xxx 10xxxxxx 10xxxxxx 10xxxxxx) */

            plane = (((src[0] & 0x07) << 2) | ((src[1] & 0x30) >> 4)) - 1;
            if (plane < 16) { /* only the first 16 planes after BMP exist in UTF-16 */
                *high = 0xD800 | 
                        (plane << 6) | 
                        ((src[1] & 0x0F) << 2) |
                        ((src[2] & 0x30) >> 4);
                *low =  0xDC00 | 
                        ((((unicode)src[2]) & 0x000F) << 6) |
                        (((unicode)src[3]) & 0x003F);
                return(4);
            }
        }
    }

    return(UTF7_STATUS_INVALID_UTF8);
}

__inline static BOOL
Copy7BitString(unsigned char **source, unsigned long sourceLen, unsigned char **dest, unsigned char *destEnd)
{
    if ((destEnd > *dest) && (sourceLen < (unsigned long)(destEnd - *dest))) {
        if (sourceLen > 0) {
            memcpy((char *)*dest, (char * )*source, sourceLen);
        
            (*dest) += sourceLen;
            (*source) += sourceLen;
        }
        return(TRUE);
    }
    return(FALSE);
}

__inline static void
Copy7BitStringNoCheck(unsigned char **source, unsigned long sourceLen, unsigned char **dest)
{

    if (sourceLen > 0) {
        memcpy((char *)*dest, (char * )*source, sourceLen);
        
        (*dest) += sourceLen;
        (*source) += sourceLen;
    }
    return;
}


__inline static int
DecodedOctetStreamToUtf8(unsigned char octet[6], unsigned long *octetCount, unsigned char **dest, unsigned char *destEnd)
{
    /* convert as many decoded bytes as possible to unicode and than utf8 */
    do {
        if ((octet[0] & 0xFC) != 0xD8) {
            unicode uniChar;

            uniChar = (unicode)(((unicode)octet[0] << 8) | (unicode)octet[1]);
            if (uniChar) {
                if (Utf8EncodeBmpChar(uniChar, dest, destEnd)) {
                    (*octetCount) -= 2;
                    memmove(octet, octet + 2, *octetCount);
                    continue; 
                }
                return(UTF7_STATUS_OUTPUT_BUFFER_TOO_SMALL);
            }

            /* too much padding */
            return(UTF7_STATUS_INVALID_UTF7);
        }

        /* validate and encode supplementary character */
        if (*octetCount > 3) {
            /* we have enough characters to look at the both the high and low surrogates */
            if ((octet[2] & 0xFC) == 0xDC) {
                if (Utf8EncodeSupplementaryChar((unicode)(((unicode)octet[0] << 8) | (unicode)octet[1]), (unicode)(((unicode)octet[2] << 8) | (unicode)octet[3]), dest, destEnd)) {
                    (*octetCount) -= 4;
                    memmove(octet, octet + 2, *octetCount);
                    continue; 
                }
                return(UTF7_STATUS_OUTPUT_BUFFER_TOO_SMALL);
            }
            return(UTF7_STATUS_INVALID_UTF7);
        }

        /* go get more */
        break;
    } while (*octetCount > 1);

    return(UTF7_STATUS_SUCCESS);
}

__inline static int
DecodedOctetStreamToUtf8NoCheck(unsigned char octet[6], unsigned long *octetCount, unsigned char **dest)
{
    /* convert as many decoded bytes as possible to unicode and than utf8 */
    do {
        if ((octet[0] & 0xFC) != 0xD8) {
            unicode uniChar;

            uniChar = (unicode)(((unicode)octet[0] << 8) | (unicode)octet[1]);
            if (uniChar) {
                Utf8EncodeBmpCharNoCheck(uniChar, dest);
                (*octetCount) -= 2;
                memmove(octet, octet + 2, *octetCount);
                continue; 
            }

            /* too much padding */
            return(UTF7_STATUS_INVALID_UTF7);
        }

        /* validate and encode supplementary character */
        if (*octetCount > 3) {
            /* we have enough characters to look at the both the high and low surrogates */
            if ((octet[2] & 0xFC) == 0xDC) {
                Utf8EncodeSupplementaryCharNoCheck((unicode)(((unicode)octet[0] << 8) | (unicode)octet[1]), (unicode)(((unicode)octet[2] << 8) | (unicode)octet[3]), dest);
                (*octetCount) -= 4;
                memmove(octet, octet + 2, *octetCount);
                continue; 
            }
            return(UTF7_STATUS_INVALID_UTF7);
        }

        /* go get more */
        break;
    } while (*octetCount > 1);

    return(UTF7_STATUS_SUCCESS);
}


__inline static int
ModifiedBase64ToUtf8(unsigned char **source, unsigned char *sourceEnd, unsigned char **dest, unsigned char *destEnd)
{
    unsigned char octet[6];
    unsigned long octetCount;
    long len;
    int ccode;

    octetCount = 0;
    len = sourceEnd - *source;

    while (len > 3) {
        /* decode next 4 bytes */
        octet[octetCount++] = ((MBASE64_R[(*source)[0]] << 2) & 0xFC) | ((MBASE64_R[(*source)[1]] >> 4) & 0x03);
        octet[octetCount++] = ((MBASE64_R[(*source)[1]] << 4) & 0xF0) | ((MBASE64_R[(*source)[2]] >> 2) & 0x0F);
        octet[octetCount++] = ((MBASE64_R[(*source)[2]] << 6) & 0xC0) | ((MBASE64_R[(*source)[3]] >> 0) & 0x3F);
        *source += 4;
        len -= 4;
        ccode = DecodedOctetStreamToUtf8(octet, &octetCount, dest, destEnd);
        if (ccode == UTF7_STATUS_SUCCESS) {
            continue;
        }
        return(ccode);
    }

    switch (len) {
        case 0: {
            if (octetCount == 0) {
                return(UTF7_STATUS_SUCCESS);
            }

            return(UTF7_STATUS_INVALID_UTF7);
        }

        case 2: {
            octet[octetCount++] = ((MBASE64_R[(*source)[0]] << 2) & 0xFC) | ((MBASE64_R[(*source)[1]] >> 4) & 0x03);
            *source += 2;
            break;
        }

        case 3: {
            octet[octetCount++] = ((MBASE64_R[(*source)[0]] << 2) & 0xFC) | ((MBASE64_R[(*source)[1]] >> 4) & 0x03);
            octet[octetCount++] = ((MBASE64_R[(*source)[1]] << 4) & 0xF0) | ((MBASE64_R[(*source)[2]] >> 2) & 0x0F);
            *source += 3;
            break;
        }

        case 1: {
            return(UTF7_STATUS_INVALID_UTF7);
        }
    }        

    ccode = DecodedOctetStreamToUtf8(octet, &octetCount, dest, destEnd);
    if (ccode == UTF7_STATUS_SUCCESS) {
        if (octetCount == 0) {
            return(UTF7_STATUS_SUCCESS);
        }

        return(UTF7_STATUS_INVALID_UTF7);
    }
    
    return(ccode);
}

__inline static int
ModifiedBase64ToUtf8NoCheck(unsigned char **source, unsigned char *sourceEnd, unsigned char **dest)
{
    unsigned char octet[6];
    unsigned long octetCount;
    long len;
    int ccode;

    octetCount = 0;
    len = sourceEnd - *source;

    while (len > 3) {
        /* decode next 4 bytes */
        octet[octetCount++] = ((MBASE64_R[(*source)[0]] << 2) & 0xFC) | ((MBASE64_R[(*source)[1]] >> 4) & 0x03);
        octet[octetCount++] = ((MBASE64_R[(*source)[1]] << 4) & 0xF0) | ((MBASE64_R[(*source)[2]] >> 2) & 0x0F);
        octet[octetCount++] = ((MBASE64_R[(*source)[2]] << 6) & 0xC0) | ((MBASE64_R[(*source)[3]] >> 0) & 0x3F);
        *source += 4;
        len -= 4;
        ccode = DecodedOctetStreamToUtf8NoCheck(octet, &octetCount, dest);
        if (ccode == UTF7_STATUS_SUCCESS) {
            continue;
        }
        return(ccode);
    }

    switch (len) {
        case 0: {
            if (octetCount == 0) {
                return(UTF7_STATUS_SUCCESS);
            }

            return(UTF7_STATUS_INVALID_UTF7);
        }

        case 2: {
            octet[octetCount++] = ((MBASE64_R[(*source)[0]] << 2) & 0xFC) | ((MBASE64_R[(*source)[1]] >> 4) & 0x03);
            *source += 2;
            break;
        }

        case 3: {
            octet[octetCount++] = ((MBASE64_R[(*source)[0]] << 2) & 0xFC) | ((MBASE64_R[(*source)[1]] >> 4) & 0x03);
            octet[octetCount++] = ((MBASE64_R[(*source)[1]] << 4) & 0xF0) | ((MBASE64_R[(*source)[2]] >> 2) & 0x0F);
            *source += 3;
            break;
        }

        case 1: {
            return(UTF7_STATUS_INVALID_UTF7);
        }
    }        

    ccode = DecodedOctetStreamToUtf8NoCheck(octet, &octetCount, dest);
    if (ccode == UTF7_STATUS_SUCCESS) {
        if (octetCount == 0) {
            return(UTF7_STATUS_SUCCESS);
        }

        return(UTF7_STATUS_INVALID_UTF7);
    }
    
    return(ccode);
}



/* Converts  modified UTF-7 as described in IMAP (RFC 3501) into UTF-8 */
/* Returns length of converted string or a negative number on failure */
int
ModifiedUtf7ToUtf8(char *utf7Start, unsigned long utf7Len, char *utf8Buffer, unsigned long utf8BufferLen)
{
    unsigned char *utf7In;
    unsigned char *utf7InEnd;

    unsigned char *utf8Out;
    unsigned char *utf8OutEnd;

    unsigned char *ptr;
    long ccode;

    utf7In = (unsigned char *)utf7Start;
    utf7InEnd = utf7In + utf7Len;

    utf8Out = (unsigned char *)utf8Buffer;
    utf8OutEnd = utf8Out + utf8BufferLen;

    do {
        /* find end of 7 bit string */
        ptr = strchr(utf7In, '&');
        if (!ptr) {
            if (Copy7BitString(&utf7In, utf7InEnd - utf7In, &utf8Out, utf8OutEnd)) {
                return((int)(utf8Out - (unsigned char *)utf8Buffer));
            }

            return(UTF7_STATUS_OUTPUT_BUFFER_TOO_SMALL);
        }

        if (Copy7BitString(&utf7In, ptr - utf7In, &utf8Out, utf8OutEnd)) {
            utf7In++;  /* skip the '&' */

            /* escaped ampersand */
            if (*utf7In == '-') {
                utf7In++;
                if (utf8Out < utf8OutEnd) {

                    *utf8Out++ = '&';
                    continue;
                }

                return(UTF7_STATUS_OUTPUT_BUFFER_TOO_SMALL);
            }

            /* have encoded string; find end */
            ptr = strchr(utf7In, '-');
            if (ptr) {
                ccode = ModifiedBase64ToUtf8(&utf7In, ptr, &utf8Out, utf8OutEnd);
                if (ccode == UTF7_STATUS_SUCCESS) {
                    utf7In++;  /* skip the trailing '-' */
                    continue;
                }

                return(ccode);
            }

            return(UTF7_STATUS_INVALID_UTF7);
        }

        return(UTF7_STATUS_OUTPUT_BUFFER_TOO_SMALL);

    } while (utf7In < utf7InEnd);

    return((int)(utf8Out - (unsigned char *)utf8Buffer));
}

/* Converts  modified UTF-7 as described in IMAP (RFC 3501) into UTF-8 */
/* The result pointer passed in will point to an allocated buffer containing the utf7 string */ 
/* Returns length of converted string or a negative number on failure */
int
GetUtf8FromModifiedUtf7(char *utf7Start, unsigned long utf7Len, char **result)
{
    unsigned char scratch[XPL_MAX_PATH * 4];
    unsigned char *utf7In;
    unsigned char *utf7InEnd;
    int resultLen;

    unsigned char *utf8Out;
    unsigned char *utf8OutBuffer;

    unsigned char *ptr;
    long ccode;

    /* Set up in and out buffers */
    utf7In = (unsigned char *)utf7Start;
    utf7InEnd = utf7In + utf7Len;
    
    if ((utf7Len * 4) < sizeof(scratch)) {
        utf8OutBuffer = scratch;
    } else {
        utf8OutBuffer = MemMalloc(utf7Len * 4);
        if (!utf8OutBuffer) {
            return(UFF7_STATUS_MEMORY_ERROR);
        }
    }
    utf8Out = utf8OutBuffer;

    do {
        /* find end of 7 bit string */
        ptr = strchr(utf7In, '&');
        if (!ptr) {
            Copy7BitStringNoCheck(&utf7In, utf7InEnd - utf7In, &utf8Out);
            resultLen = (int)(utf8Out - (unsigned char *)utf8OutBuffer);
            *result = MemMalloc(resultLen + 1);
            if (*result) {
                memcpy(*result, utf8OutBuffer, resultLen);
                (*result)[resultLen] = '\0';
                if (utf8OutBuffer == scratch) {
                    return(resultLen);
                }
                MemFree(utf8OutBuffer);
                return(resultLen);
            }
            if (utf8OutBuffer != scratch) {
                MemFree(utf8OutBuffer);
            }
            return(UFF7_STATUS_MEMORY_ERROR);
        }

        Copy7BitStringNoCheck(&utf7In, ptr - utf7In, &utf8Out);
        utf7In++;  /* skip the '&' */

        /* escaped ampersand */
        if (*utf7In == '-') {
            utf7In++;
            *utf8Out++ = '&';
            continue;
        }

        /* have encoded string; find end */
        ptr = strchr(utf7In, '-');
        if (ptr) {
            ccode = ModifiedBase64ToUtf8NoCheck(&utf7In, ptr, &utf8Out);
            if (ccode == UTF7_STATUS_SUCCESS) {
                utf7In++;  /* skip the trailing '-' */
                continue;
            }
            if (utf8OutBuffer != scratch) {
                MemFree(utf8OutBuffer);
            }
            return(ccode);
        }

        if (utf8OutBuffer != scratch) {
            MemFree(utf8OutBuffer);
        }
        return(UTF7_STATUS_INVALID_UTF7);

    } while (utf7In < utf7InEnd);

    resultLen = (int)(utf8Out - (unsigned char *)utf8OutBuffer);

    *result = MemMalloc(resultLen + 1);
    if(*result) {
        memcpy(*result, utf8OutBuffer, resultLen);
        (*result)[resultLen] = '\0';
        if (utf8OutBuffer == scratch) {
            return(resultLen);
        }
        MemFree(utf8OutBuffer);
        return(resultLen);
    }
    if (utf8OutBuffer != scratch) {
        MemFree(utf8OutBuffer);
    }
    return(UFF7_STATUS_MEMORY_ERROR);
}


/* Converts UTF-8 steams into modified UTF-7 as described in IMAP (RFC 3501) */
/* Returns length of converted string or a negative number on failure */
int
Utf8ToModifiedUtf7(char *utf8Start, unsigned long utf8Len, char *utf7Buffer, unsigned long utf7BufferLen)
{
    unsigned char *utf8In;
    unsigned char *utf8InEnd;

    long utf8CharLen;
    unicode utf16high;
    unicode utf16low;
  
    unsigned char *utf7Out;
    unsigned char *utf7OutEnd;
  
    BOOL PassThroughMode;

    unsigned char octet[6];
    long octetCount = 0;

    utf8In = (unsigned char *)utf8Start;
    utf8InEnd = utf8In + utf8Len;
    
    utf7Out = (unsigned char *)utf7Buffer;
    utf7OutEnd = utf7Out + utf7BufferLen;
    

    PassThroughMode = TRUE;

    do {
        if (PassThroughMode) {
            do {
                if ((*utf8In > 0x1F) && (*utf8In < 0x7F)) {
                    if (*utf8In != '&') {
                        if (utf7Out < utf7OutEnd) {
                            /* these characters get passed straight through */
                            *utf7Out++ = *utf8In++;
                            continue;
                        }
                        return(UTF7_STATUS_OUTPUT_BUFFER_TOO_SMALL);
                    }
                    /* escape ampersands as per rfc 3501 */
                    if ((utf7Out + 1) < utf7OutEnd) {
                        utf8In++;
                        *utf7Out++ = '&';
                        *utf7Out++ = '-';
                        continue;
                    }
                    return(UTF7_STATUS_OUTPUT_BUFFER_TOO_SMALL);
                }

                /* we need to encode this character */
                PassThroughMode = FALSE;
                break;
            } while (utf8In < utf8InEnd);

            continue;
        }

        /* begin encoded string */
        *utf7Out++ = '&';

        do {
            /* convert the UTF8 character to UTF16 */
            utf8CharLen = Utf8CharToUtf16(utf8In, utf8InEnd, &utf16high, &utf16low);

            if ((utf8CharLen == 2) || (utf8CharLen == 3)) {
                octet[octetCount++] = (unsigned char)((utf16low & 0xff00) >> 8);
                octet[octetCount++] = (unsigned char)(utf16low & 0x00ff);
                utf8In += utf8CharLen;
                if (ModifiedBase64Encode3Bytes(octet, &octetCount, &utf7Out, utf7OutEnd)) {
                    continue;
                }
                return(UTF7_STATUS_OUTPUT_BUFFER_TOO_SMALL);
            }

            if (utf8CharLen == 1) {
                if ((*utf8In < 0x20) || (*utf8In > 0x7E)) {
                    /* these still need encoded */
                    octet[octetCount++] = 0;
                    octet[octetCount++] = *utf8In;
                    utf8In++;
                    if (ModifiedBase64Encode3Bytes(octet, &octetCount, &utf7Out, utf7OutEnd)) {
                        continue;
                    }
                    return(UTF7_STATUS_OUTPUT_BUFFER_TOO_SMALL);
                }

                PassThroughMode = TRUE;
                break;
            }

            if (utf8CharLen == 4) {
                octet[octetCount++] = (unsigned char)((utf16high & 0xff00) >> 8);
                octet[octetCount++] = (unsigned char)(utf16high & 0x00ff);
                octet[octetCount++] = (unsigned char)((utf16low & 0xff00) >> 8);
                octet[octetCount++] = (unsigned char)(utf16low & 0x00ff);
                utf8In += 4;
                /* Call function twice because there could be up to six bytes to encode */
                if (ModifiedBase64Encode3Bytes(octet, &octetCount, &utf7Out, utf7OutEnd) &&
                    ModifiedBase64Encode3Bytes(octet, &octetCount, &utf7Out, utf7OutEnd)) {
                    continue;
                }
                return(UTF7_STATUS_OUTPUT_BUFFER_TOO_SMALL);
            }

            return(UTF7_STATUS_INVALID_UTF8);
        } while (utf8In < utf8InEnd);

        /* pad and flush any unwritten encoding */
        if (ModifiedBase64EncodeLeftovers(octet, &octetCount, &utf7Out, utf7OutEnd)) {
            if (utf7Out < utf7OutEnd) {
                *utf7Out++ = '-';
                continue;
            }
        }
        return(UTF7_STATUS_OUTPUT_BUFFER_TOO_SMALL);
    } while (utf8In < utf8InEnd);

    return((int)(utf7Out - (unsigned char *)utf7Buffer));
}


/* Converts UTF-8 steams into modified UTF-7 as described in IMAP (RFC 3501) */
/* The result pointer passed in will point to an allocated buffer containing the utf7 string */ 
/* Returns length of converted string or a negative number on failure */
int
GetModifiedUtf7FromUtf8(char *utf8Start, unsigned long utf8Len, char **result)
{
    unsigned char scratch[XPL_MAX_PATH * 4];
    unsigned char *utf8In;
    unsigned char *utf8InEnd;
    unsigned char *utf7Out;
    unsigned char *utf7OutBuffer;
    int resultLen;
    long utf8CharLen;
    unicode utf16high;
    unicode utf16low;
    BOOL PassThroughMode = TRUE;
    unsigned char octet[6];
    long octetCount = 0;

    /* Set up in and out buffers */
    utf8In = (unsigned char *)utf8Start;
    utf8InEnd = utf8In + utf8Len;
    
    if ((utf8Len * 4) < sizeof(scratch)) {
        utf7OutBuffer = scratch;
    } else {
        utf7OutBuffer = MemMalloc(utf8Len * 4);
        if (!utf7OutBuffer) {
            return(UFF7_STATUS_MEMORY_ERROR);
        }
    }
    utf7Out = utf7OutBuffer;

    do {
        if (PassThroughMode) {
            do {
                if ((*utf8In > 0x1F) && (*utf8In < 0x7F)) {
                    if (*utf8In != '&') {
                        /* these characters get passed straight through */
                        *utf7Out++ = *utf8In++;
                        continue;
                    }
                    /* escape ampersands as per rfc 3501 */
                    utf8In++;
                    *utf7Out++ = '&';
                    *utf7Out++ = '-';
                    continue;
                }

                /* we need to encode this character */
                PassThroughMode = FALSE;
                break;
            } while (utf8In < utf8InEnd);

            continue;
        }

        /* begin encoded string */
        *utf7Out++ = '&';

        do {
            /* convert the UTF8 character to UTF16 */
            utf8CharLen = Utf8CharToUtf16(utf8In, utf8InEnd, &utf16high, &utf16low);

            if ((utf8CharLen == 2) || (utf8CharLen == 3)) {
                octet[octetCount++] = (unsigned char)((utf16low & 0xff00) >> 8);
                octet[octetCount++] = (unsigned char)(utf16low & 0x00ff);
                utf8In += utf8CharLen;
                ModifiedBase64Encode3BytesNoCheck(octet, &octetCount, &utf7Out);
                continue;
            }

            if (utf8CharLen == 1) {
                if ((*utf8In < 0x20) || (*utf8In > 0x7E)) {
                    /* these still need encoded */
                    octet[octetCount++] = 0;
                    octet[octetCount++] = *utf8In;
                    utf8In++;
                    ModifiedBase64Encode3BytesNoCheck(octet, &octetCount, &utf7Out);
                    continue;
                }

                PassThroughMode = TRUE;
                break;
            }

            if (utf8CharLen == 4) {
                octet[octetCount++] = (unsigned char)((utf16high & 0xff00) >> 8);
                octet[octetCount++] = (unsigned char)(utf16high & 0x00ff);
                octet[octetCount++] = (unsigned char)((utf16low & 0xff00) >> 8);
                octet[octetCount++] = (unsigned char)(utf16low & 0x00ff);
                utf8In += 4;
                /* Call function twice because there could be up to six bytes to encode */
                ModifiedBase64Encode3BytesNoCheck(octet, &octetCount, &utf7Out);
                ModifiedBase64Encode3BytesNoCheck(octet, &octetCount, &utf7Out);
                continue;
            }

            if (utf7OutBuffer == scratch) {
                return(UTF7_STATUS_INVALID_UTF8);
            }
            MemFree(utf7OutBuffer);
            return(UTF7_STATUS_INVALID_UTF8);
            
        } while (utf8In < utf8InEnd);

        /* pad and flush any unwritten encoding */
        ModifiedBase64EncodeLeftoversNoCheck(octet, &octetCount, &utf7Out);
        *utf7Out++ = '-';
    } while (utf8In < utf8InEnd);
    
    resultLen = utf7Out - utf7OutBuffer;
    *result = MemMalloc(resultLen + 1);
    if (*result) {
        memcpy(*result, utf7OutBuffer, resultLen);
        (*result)[resultLen] = '\0';
        if (utf7OutBuffer == scratch) {
            return(resultLen);
        }
        MemFree(utf7OutBuffer);
        return(resultLen);
    }

    if (utf7OutBuffer == scratch) {
        return(UFF7_STATUS_MEMORY_ERROR);
    }
    MemFree(utf7OutBuffer);
    return(UFF7_STATUS_MEMORY_ERROR);
}

int
ValidateModifiedUtf7(char *utf7String)
{
    int len;
    char *utf8String;
    char *resultUtf7;

    len = GetUtf8FromModifiedUtf7(utf7String, strlen(utf7String), &utf8String);
    if (len > 0) {
        len = GetModifiedUtf7FromUtf8(utf8String, len, &resultUtf7);
        if (len > 0) {
            if (strcmp(utf7String, resultUtf7) == 0) {
                MemFree(utf8String);
                MemFree(resultUtf7);
                return(UTF7_STATUS_SUCCESS);
            }
            MemFree(resultUtf7);
        }
        MemFree(utf8String);
    }
    if (len < 0) {
        return(len);
    }
    return(UTF7_STATUS_INVALID_UTF7);
}


int
ValidateUtf8(char *utf8String)
{
    int len = 0;
    char *resultUtf7;

    len = GetModifiedUtf7FromUtf8(utf8String, len, &resultUtf7);
    if (len > 0) {
        MemFree(resultUtf7);
        return(UTF7_STATUS_SUCCESS);
    }
    if (len < 0) {
        return(len);
    }
    return(UTF7_STATUS_INVALID_UTF8);
}

int 
ValidateUtf7Utf8Pair(char *utf7String, size_t utf7StringLen, char *utf8String, size_t utf8StringLen)
{
    int len;
    char *resultUtf7;

    len = GetModifiedUtf7FromUtf8(utf8String, utf8StringLen, &resultUtf7);
    if (len > 0) {
        if (len == (int)utf7StringLen) {
            if (strcmp(utf7String, resultUtf7) == 0) {
                MemFree(resultUtf7);
                return(UTF7_STATUS_SUCCESS);
            }
        }
        MemFree(resultUtf7);
    }
    if (len < 0) {
        return(len);
    }
    return(UTF7_STATUS_INVALID_UTF7);
}
