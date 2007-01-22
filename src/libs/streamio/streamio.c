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
#include <string.h>
#include <stdio.h>
#include <xpl.h>
#include <bongoutil.h>
#include "streamio.h"
#include "streamiop.h"

/***************************************************************************
 CHARACTER SETS AND OTHER TRANSLATORS
***************************************************************************/

#include "codec/ascii.h"

/* Generic non-charset encodings */
#include "codec/quoted_string.h"
#include "codec/base64.h"
#include "codec/q.h"
#include "codec/qp.h"
#include "codec/rfc1522.h"
#include "codec/rfc2231.h"
#include "codec/html.h"
#include "codec/wml.h"
#include "codec/vcard.h"
#include "codec/crlf.h"
#include "codec/search.h"
#include "codec/rfc822.h"
/* General multi-byte encodings */
#include "codec/utf8.h"
#include "codec/ucs_2.h"
#include "codec/ucs_4.h"

/* 8-bit encodings */
#include "codec/iso8859_1.h"
#include "codec/iso8859_2.h"
#include "codec/iso8859_3.h"
#include "codec/iso8859_4.h"
#include "codec/iso8859_5.h"
#include "codec/iso8859_6.h"
#include "codec/iso8859_7.h"
#include "codec/iso8859_8.h"
#include "codec/iso8859_9.h"
#include "codec/iso8859_10.h"
#include "codec/iso8859_13.h"
#include "codec/iso8859_14.h"
#include "codec/iso8859_15.h"
#include "codec/iso8859_16.h"
#include "codec/koi8_r.h"
#include "codec/koi8_u.h"
#include "codec/koi8_ru.h"
#include "codec/cp850.h"
#include "codec/cp862.h"
#include "codec/cp866.h"
#include "codec/cp874.h"
#include "codec/cp1250.h"
#include "codec/cp1251.h"
#include "codec/cp1252.h"
#include "codec/cp1253.h"
#include "codec/cp1254.h"
#include "codec/cp1255.h"
#include "codec/cp1256.h"
#include "codec/cp1257.h"
#include "codec/cp1258.h"
#include "codec/cp1133.h"
#include "codec/viscii.h"
#include "codec/tis620.h"

/* CJK character sets [CCS = coded character set] [CJKV.INF chapter 3] */
#include "codec/ksc5601.h"

/* CJK encodings [CES = character encoding scheme] [CJKV.INF chapter 4] */
#include "codec/cns11643.h"

#include "codec/iso2022_kr.h"
#include "codec/euc_tw.h"
#include "codec/euc_kr.h"

#include "codec/gb2312.h"
#include "codec/iso2022_cn.h"
#include "codec/hz.h"
#include "codec/big5.h"

#include "codec/jisx0208.h"
#include "codec/sjis.h"
#include "codec/iso2022_jp.h"

#if 0
/* General multi-byte encodings */
#include "codec/ucs2.h"
#include "codec/ucs2be.h"
#include "codec/ucs2le.h"
#include "codec/ucs4.h"
#include "codec/ucs4be.h"
#include "codec/ucs4le.h"
#include "codec/utf16.h"
#include "codec/utf16be.h"
#include "codec/utf16le.h"
#include "codec/utf7.h"
#include "codec/ucs2internal.h"
#include "codec/ucs2swapped.h"
#include "codec/ucs4internal.h"
#include "codec/ucs4swapped.h"
#include "codec/java.h"

/* 8-bit encodings */
#include "codec/mac_roman.h"
#include "codec/mac_centraleurope.h"
#include "codec/mac_iceland.h"
#include "codec/mac_croatian.h"
#include "codec/mac_romania.h"
#include "codec/mac_cyrillic.h"
#include "codec/mac_ukraine.h"
#include "codec/mac_greek.h"
#include "codec/mac_turkish.h"
#include "codec/mac_hebrew.h"
#include "codec/mac_arabic.h"
#include "codec/mac_thai.h"
#include "codec/hp_roman8.h"
#include "codec/nextstep.h"
#include "codec/armscii_8.h"
#include "codec/georgian_academy.h"
#include "codec/georgian_ps.h"
#include "codec/mulelao.h"
#include "codec/tcvn.h"

/* CJK character sets [CCS = coded character set] [CJKV.INF chapter 3] */

#include "codec/iso646_jp.h"
#include "codec/jisx0201.h"
#include "codec/jisx0208.h"
#include "codec/jisx0212.h"

#include "codec/iso646_cn.h"
#include "codec/isoir165.h"
/*#include "codec/gb12345.h"*/
#include "codec/gbk.h"
#include "codec/cns11643.h"

#include "codec/johab_hangul.h"


/* CJK encodings [CES = character encoding scheme] [CJKV.INF chapter 4] */

#include "codec/euc_jp.h"
#include "codec/cp932.h"
#include "codec/iso2022_jp1.h"
#include "codec/iso2022_jp2.h"

#include "codec/euc_cn.h"
#include "codec/ces_gbk.h"
#include "codec/gb18030.h"
#include "codec/iso2022_cnext.h"
#include "codec/cp950.h"
#include "codec/big5hkscs.h"

#include "codec/cp949.h"
#include "codec/johab.h"
#endif

#if 0
LONG							Tid;
LONG							TGid;
#endif

BongoKeywordIndex *CodecIndex = NULL;

StreamDescStruct StreamList[] = {
/* Special "charsets" */
{"RFC1522", RFC1522_Decode, RFC1522_Encode, FALSE, NULL, },
{"BASE64", BASE64_Decode, BASE64_Encode, FALSE, NULL, },
{"quoted-printable", QP_Decode, QP_Encode, FALSE, NULL, }, 
{"Q", Q_Decode, Q_Encode, FALSE, NULL, },
{"text/html", HTML_Decode, NULL, FALSE, NULL, },
{"text/plain", HTML_Encode, NULL, FALSE, NULL, },
{"text/wml", WML_Encode, NULL, FALSE, NULL, },
{"7bit", NULL, NULL, FALSE, NULL, },
{"8bit", NULL, NULL, FALSE, NULL, },
{"RFC2047_Name", NULL, RFC2047_Name_Encode, FALSE, NULL, },
{"RFC2231_7bit", RFC2231_7Bit_Decode, RFC2231_7Bit_Encode, FALSE, NULL, },
{"RFC2231_Param_Value", NULL, RFC2231_Value_Encode, FALSE, NULL, },
{"RFC2231_NMAP_Value", RFC2231_NMAP_Decode, RFC2231_NMAP_Encode, FALSE, NULL, },
{"quoted-string", Quoted_String_Decode, Quoted_String_Encode, FALSE, NULL, },
{"filename", Filename_Decode, Filename_Encode, FALSE, NULL, },
{"RFC1522Q", RFC1522_Q_Decode, RFC1522_Q_Encode, FALSE, NULL, },
{"RFC1522B", RFC1522_B_Decode, RFC1522_B_Encode, FALSE, NULL, },
//{"text/x-vcard", VCard_Decode, NULL, FALSE, NULL, },
{"CRLF", NULL, CRLF_Encode, FALSE, NULL, }, 
{"html_text", HTML_Text_Decode, NULL, FALSE, NULL, }, 
{"rfc822_fold", RFC822_Fold_Decode, NULL, FALSE, NULL, }, 
{"rfc822_header_value", RFC822_Header_Value_Decode, NULL, FALSE, NULL, }, 
{"search_substring", Search_Substring_Decode, NULL, FALSE, NULL, }, 
{"search_subword", Search_Subword_Decode, NULL, FALSE, NULL, }, 

/*Regular charets,  in order of most to least likely to occur;for speed*/
{"UTF-8", UTF8_Decode, UTF8_Encode, TRUE, NULL, },
{"UCS-2", UCS_2_Decode, UCS_2_Encode, TRUE, NULL, },
{"UCS-4", UCS_4_Decode, UCS_4_Encode, TRUE, NULL, },

{"US-ASCII", ASCII_Decode, ASCII_Encode, TRUE, NULL, },

{"ISO-8859-1", ISO8859_1_Decode, ISO8859_1_Encode, TRUE, NULL, },
{"ISO-8859-2", ISO8859_2_Decode, ISO8859_2_Encode, TRUE, NULL, },
{"ISO-8859-3", ISO8859_3_Decode, ISO8859_3_Encode, TRUE, NULL, },
{"ISO-8859-4", ISO8859_4_Decode, ISO8859_4_Encode, TRUE, NULL, },
{"ISO-8859-5", ISO8859_5_Decode, ISO8859_5_Encode, TRUE, NULL, },
{"ISO-8859-6", ISO8859_6_Decode, ISO8859_6_Encode, TRUE, NULL, },
{"ISO-8859-7", ISO8859_7_Decode, ISO8859_7_Encode, TRUE, NULL, },
{"ISO-8859-8", ISO8859_8_Decode, ISO8859_8_Encode, TRUE, NULL, },
{"ISO-8859-9", ISO8859_9_Decode, ISO8859_9_Encode, TRUE, NULL, },
{"ISO-8859-10", ISO8859_10_Decode, ISO8859_10_Encode, TRUE, NULL, },
{"ISO-8859-13", ISO8859_13_Decode, ISO8859_13_Encode, TRUE, NULL, },
{"ISO-8859-14", ISO8859_14_Decode, ISO8859_14_Encode, TRUE, NULL, },
{"ISO-8859-15", ISO8859_15_Decode, ISO8859_15_Encode, TRUE, NULL, },
{"ISO-8859-16", ISO8859_16_Decode, ISO8859_16_Encode, TRUE, NULL, },

{"KOI8-R", KOI8_R_Decode, KOI8_R_Encode, TRUE, NULL, },
{"KOI8-U", KOI8_U_Decode, KOI8_U_Encode, TRUE, NULL, },

{"WINDOWS-1250", CP1250_Decode, CP1250_Encode, TRUE, NULL, },
{"WINDOWS-1251", CP1251_Decode, CP1251_Encode, TRUE, NULL, },
{"WINDOWS-1252", CP1252_Decode, CP1252_Encode, TRUE, NULL, },
{"WINDOWS-1253", CP1253_Decode, CP1253_Encode, TRUE, NULL, },
{"WINDOWS-1254", CP1254_Decode, CP1254_Encode, TRUE, NULL, },
{"WINDOWS-1255", CP1255_Decode, CP1255_Encode, TRUE, NULL, },
{"WINDOWS-1256", CP1256_Decode, CP1256_Encode, TRUE, NULL, },
{"WINDOWS-1257", CP1257_Decode, CP1257_Encode, TRUE, NULL, },
{"WINDOWS-1258", CP1258_Decode, CP1258_Encode, TRUE, NULL, },

{"EUC-KR", EUC_KR_Decode, EUC_KR_Encode, TRUE, NULL, },
{"KS_C_5601-1987", EUC_KR_Decode, EUC_KR_Encode, TRUE, NULL, },

{"ISO-2022-KR", ISO2022_KR_Decode, ISO2022_KR_Encode, TRUE, NULL, },

{"ISO-2022-CN", ISO2022_CN_Decode, ISO2022_CN_Encode, TRUE, NULL, },

{"GB2312", GB2312_Decode, GB2312_Encode, TRUE, NULL, },

{"BIG5", BIG5_Decode, BIG5_Encode, TRUE, NULL, },

{"ISO-2022-JP", ISO2022_JP_Decode, ISO2022_JP_Encode, TRUE, NULL, },

{"SHIFT_JIS", SJIS_Decode, SJIS_Encode, TRUE, NULL, },

{"CP850", CP850_Decode, CP850_Encode, TRUE, NULL, },

//{"CP862", CP862_Decode, CP862_Encode, TRUE, NULL, },

{"CP866", CP866_Decode, CP866_Encode, TRUE, NULL, },

{"VISCII", VISCII_Decode, VISCII_Encode, TRUE, NULL, },

{"TIS-620", TIS620_Decode, TIS620_Encode, TRUE, NULL, },

/* These charset names are not IANA approved (http://www.iana.org/assignments/character-sets) */
/* What names should bongo be using? */
{"KOI8-RU", KOI8_RU_Decode, KOI8_RU_Encode, TRUE, NULL, },
{"EUC-TW", EUC_TW_Decode, EUC_TW_Encode, TRUE, NULL, },
{"CP874", CP874_Decode, CP874_Encode, TRUE, NULL, },
{"CP1133", CP1133_Decode, CP1133_Encode, TRUE, NULL, },


/*These charsets are the same as above entries, but with less common charset names*/

/*US-ASCII*/
{"ASCII", ASCII_Decode, ASCII_Encode, FALSE, NULL, },
{"ISO-646-US", ASCII_Decode, ASCII_Encode, FALSE, NULL, },
{"ISO_646.IRV:1991", ASCII_Decode, ASCII_Encode, FALSE, NULL, },
{"ISO-IR-6", ASCII_Decode, ASCII_Encode, FALSE, NULL, },
{"ANSI_X3.4-1968", ASCII_Decode, ASCII_Encode, FALSE, NULL, },
{"CP367", ASCII_Decode, ASCII_Encode, FALSE, NULL, },
{"IBM367", ASCII_Decode, ASCII_Encode, FALSE, NULL, },
{"US", ASCII_Decode, ASCII_Encode, FALSE, NULL, },
{"CSASCII", ASCII_Decode, ASCII_Encode, FALSE, NULL, },

/*ISO8859-1*/
{"ISO_8859-1:1987", ISO8859_1_Decode, ISO8859_1_Encode, FALSE, NULL, },
{"ISO-IR-100", ISO8859_1_Decode, ISO8859_1_Encode, FALSE, NULL, },
{"CP819", ISO8859_1_Decode, ISO8859_1_Encode, FALSE, NULL, },
{"IBM819", ISO8859_1_Decode, ISO8859_1_Encode, FALSE, NULL, },
{"LATIN1", ISO8859_1_Decode, ISO8859_1_Encode, FALSE, NULL, },
{"L1", ISO8859_1_Decode, ISO8859_1_Encode, FALSE, NULL, },
{"CISOLATIN1", ISO8859_1_Decode, ISO8859_1_Encode, FALSE, NULL, },

/*ISO8859-2*/
{"ISO_8859-2", ISO8859_2_Decode, ISO8859_2_Encode, FALSE, NULL, },
{"ISO_8859-2:1987", ISO8859_2_Decode, ISO8859_2_Encode, FALSE, NULL, },
{"ISO-IR-101", ISO8859_2_Decode, ISO8859_2_Encode, FALSE, NULL, },
{"LATIN2", ISO8859_2_Decode, ISO8859_2_Encode, FALSE, NULL, },
{"L2", ISO8859_2_Decode, ISO8859_2_Encode, FALSE, NULL, },
{"CSISOLATIN2", ISO8859_2_Decode, ISO8859_2_Encode, FALSE, NULL, },

/*ISO8859-3*/
{"ISO_8859-3", ISO8859_3_Decode, ISO8859_3_Encode, FALSE, NULL, },
{"ISO_8859-3:1988", ISO8859_3_Decode, ISO8859_3_Encode, FALSE, NULL, },
{"ISO-IR-109", ISO8859_3_Decode, ISO8859_3_Encode, FALSE, NULL, },
{"LATIN3", ISO8859_3_Decode, ISO8859_3_Encode, FALSE, NULL, },
{"L3", ISO8859_3_Decode, ISO8859_3_Encode, FALSE, NULL, },
{"CSISOLATIN3", ISO8859_3_Decode, ISO8859_3_Encode, FALSE, NULL, },

/*ISO8859-4*/
{"ISO_8859-4", ISO8859_4_Decode, ISO8859_4_Encode, FALSE, NULL, },
{"ISO_8859-4:1988", ISO8859_4_Decode, ISO8859_4_Encode, FALSE, NULL, },
{"ISO-IR-110", ISO8859_4_Decode, ISO8859_4_Encode, FALSE, NULL, },
{"LATIN4", ISO8859_4_Decode, ISO8859_4_Encode, FALSE, NULL, },
{"L4", ISO8859_4_Decode, ISO8859_4_Encode, FALSE, NULL, },
{"CSISOLATIN4", ISO8859_4_Decode, ISO8859_4_Encode, FALSE, NULL, },

/*ISO8859-5*/
{"ISO_8859-5", ISO8859_5_Decode, ISO8859_5_Encode, FALSE, NULL, },
{"ISO_8859-5:1988", ISO8859_5_Decode, ISO8859_5_Encode, FALSE, NULL, },
{"ISO-IR-144", ISO8859_5_Decode, ISO8859_5_Encode, FALSE, NULL, },
{"CYRILLIC", ISO8859_5_Decode, ISO8859_5_Encode, FALSE, NULL, },
{"CSISOLATINCYRILLIC", ISO8859_5_Decode, ISO8859_5_Encode, FALSE, NULL, },

/*ISO8859-6*/
{"ISO_8859-6", ISO8859_6_Decode, ISO8859_6_Encode, FALSE, NULL, },
{"ISO_8859-6:1987", ISO8859_6_Decode, ISO8859_6_Encode, FALSE, NULL, },
{"ISO-IR-127", ISO8859_6_Decode, ISO8859_6_Encode, FALSE, NULL, },
{"ECMA-114", ISO8859_6_Decode, ISO8859_6_Encode, FALSE, NULL, },
{"ASMO-708", ISO8859_6_Decode, ISO8859_6_Encode, FALSE, NULL, },
{"ARABIC", ISO8859_6_Decode, ISO8859_6_Encode, FALSE, NULL, },
{"CSISOLATINARABIC", ISO8859_6_Decode, ISO8859_6_Encode, FALSE, NULL, },

/*ISO8859-7*/
{"ISO_8859-7", ISO8859_7_Decode, ISO8859_7_Encode, FALSE, NULL, },
{"ISO_8859-7:1987", ISO8859_7_Decode, ISO8859_7_Encode, FALSE, NULL, },
{"ISO-IR-126", ISO8859_7_Decode, ISO8859_7_Encode, FALSE, NULL, },
{"ECMA-118", ISO8859_7_Decode, ISO8859_7_Encode, FALSE, NULL, },
{"ELOT_928", ISO8859_7_Decode, ISO8859_7_Encode, FALSE, NULL, },
{"GREEK8", ISO8859_7_Decode, ISO8859_7_Encode, FALSE, NULL, },
{"GREEK", ISO8859_7_Decode, ISO8859_7_Encode, FALSE, NULL, },
{"CSISOLATINGREEK", ISO8859_7_Decode, ISO8859_7_Encode, FALSE, NULL, },

/*ISO8859-8*/
{"ISO_8859-8", ISO8859_8_Decode, ISO8859_8_Encode, FALSE, NULL, },
{"ISO_8859-8:1988", ISO8859_8_Decode, ISO8859_8_Encode, FALSE, NULL, },
{"ISO-IR-138", ISO8859_8_Decode, ISO8859_8_Encode, FALSE, NULL, },
{"HEBREW", ISO8859_8_Decode, ISO8859_8_Encode, FALSE, NULL, },
{"CSISOLATINHEBREW", ISO8859_8_Decode, ISO8859_8_Encode, FALSE, NULL, },

/*ISO8859-9*/
{"ISO_8859-9", ISO8859_9_Decode, ISO8859_9_Encode, FALSE, NULL, },
{"ISO_8859-9:1989", ISO8859_9_Decode, ISO8859_9_Encode, FALSE, NULL, },
{"ISO-IR-148", ISO8859_9_Decode, ISO8859_9_Encode, FALSE, NULL, },
{"LATIN5", ISO8859_9_Decode, ISO8859_9_Encode, FALSE, NULL, },
{"L5", ISO8859_9_Decode, ISO8859_9_Encode, FALSE, NULL, },
{"CSISOLATIN5", ISO8859_9_Decode, ISO8859_9_Encode, FALSE, NULL, },

/*ISO8859-10*/
{"ISO_8859-10", ISO8859_10_Decode, ISO8859_10_Encode, FALSE, NULL, },
{"ISO_8859-10:1992", ISO8859_10_Decode, ISO8859_10_Encode, FALSE, NULL, },
{"ISO-IR-157", ISO8859_10_Decode, ISO8859_10_Encode, FALSE, NULL, },
{"LATIN6", ISO8859_10_Decode, ISO8859_10_Encode, FALSE, NULL, },
{"L6", ISO8859_10_Decode, ISO8859_10_Encode, FALSE, NULL, },
{"CSISOLATIN6", ISO8859_10_Decode, ISO8859_10_Encode, FALSE, NULL, },

/*ISO8859-13*/
{"ISO_8859-13", ISO8859_13_Decode, ISO8859_13_Encode, FALSE, NULL, },
{"ISO-IR-179", ISO8859_13_Decode, ISO8859_13_Encode, FALSE, NULL, },
{"LATIN7", ISO8859_13_Decode, ISO8859_13_Encode, FALSE, NULL, },
{"L7", ISO8859_13_Decode, ISO8859_13_Encode, FALSE, NULL, },

/*ISO8859-14*/
{"ISO_8859-14", ISO8859_14_Decode, ISO8859_14_Encode, FALSE, NULL, },
{"ISO_8859-14:1998", ISO8859_14_Decode, ISO8859_14_Encode, FALSE, NULL, },
{"ISO-IR-199", ISO8859_14_Decode, ISO8859_14_Encode, FALSE, NULL, },
{"LATIN8", ISO8859_14_Decode, ISO8859_14_Encode, FALSE, NULL, },
{"L8", ISO8859_14_Decode, ISO8859_14_Encode, FALSE, NULL, },

/*ISO8859-15*/
{"ISO_8859-15", ISO8859_15_Decode, ISO8859_15_Encode, FALSE, NULL, },
{"ISO_8859-15:1998", ISO8859_15_Decode, ISO8859_15_Encode, FALSE, NULL, },
{"ISO-IR-203", ISO8859_15_Decode, ISO8859_15_Encode, FALSE, NULL, },

/*ISO8859-16*/
{"ISO_8859-16", ISO8859_16_Decode, ISO8859_16_Encode, FALSE, NULL, },
{"ISO_8859-16:2000", ISO8859_16_Decode, ISO8859_16_Encode, FALSE, NULL, },
{"ISO-IR-226", ISO8859_16_Decode, ISO8859_16_Encode, FALSE, NULL, },

/*KOI8*/
{"CSKOI8R", KOI8_R_Decode, KOI8_R_Encode, FALSE, NULL, },


/*Korean*/
{"EUCKR", EUC_KR_Decode, EUC_KR_Encode, FALSE, NULL, },
{"CSEUCKR", EUC_KR_Decode, EUC_KR_Encode, FALSE, NULL, },
{"CSISO2022KR", ISO2022_KR_Decode, ISO2022_KR_Encode, FALSE, NULL, },

/*ChineseGB-2312*/
{"GB_2312", GB2312_Decode, GB2312_Encode, FALSE, NULL, },
{"GB_2312-80", GB2312_Decode, GB2312_Encode, FALSE, NULL, },
{"CSISO58GB231280", GB2312_Decode, GB2312_Encode, FALSE, NULL, },
{"CHINESE", GB2312_Decode, GB2312_Encode, FALSE, NULL, },

/*ChineseHZ-GB-2312*/
{"HZ", HZ_Decode, HZ_Encode, FALSE, NULL, },
{"HZ-GB-2312", HZ_Decode, HZ_Encode, FALSE, NULL, },

/*ChineseISO2022-CN*/
{"CSISO2022CN", ISO2022_CN_Decode, ISO2022_CN_Encode, FALSE, NULL, },

/*ChineseBIG5*/
{"BIG-5", BIG5_Decode, BIG5_Encode, FALSE, NULL, },
{"BIG-FIVE", BIG5_Decode, BIG5_Encode, FALSE, NULL, },
{"BIGFIVE", BIG5_Decode, BIG5_Encode, FALSE, NULL, },
{"CN-BIG5", BIG5_Decode, BIG5_Encode, FALSE, NULL, },
{"CSBIG5", BIG5_Decode, BIG5_Encode, FALSE, NULL, },


/*TaiwanEUC*/
{"EUCTW", EUC_TW_Decode, EUC_TW_Encode, FALSE, NULL, },
{"CSEUCTW", EUC_TW_Decode, EUC_TW_Encode, FALSE, NULL, },

/*JapaneseShift-JIS*/
{"SJIS", SJIS_Decode, SJIS_Encode, FALSE, NULL, },
{"SHIFT-JIS", SJIS_Decode, SJIS_Encode, FALSE, NULL, },
{"CSSHIFTJIS", SJIS_Decode, SJIS_Encode, FALSE, NULL, },
{"MS_KANJI", SJIS_Decode, SJIS_Encode, FALSE, NULL, },

/*JapanaeseISO2022*/
{"CSISO2022JP", ISO2022_JP_Decode, ISO2022_JP_Encode, FALSE, NULL, },

/*Windowscodepages*/
{"IBM850", CP850_Decode, CP850_Encode, FALSE, NULL, },
{"850", CP850_Decode, CP850_Encode, FALSE, NULL, },
{"CSPC850MULTILINGUAL", CP850_Decode, CP850_Encode, FALSE, NULL, },
//{"IBM862", CP862_Decode, CP862_Encode, FALSE, NULL, },
//{"862", CP862_Decode, CP862_Encode, FALSE, NULL, },
//{"CSPC862LATINHEBREW", CP862_Decode, CP862_Encode, FALSE, NULL, },
{"IBM866", CP866_Decode, CP866_Encode, FALSE, NULL, },
{"866", CP866_Decode, CP866_Encode, FALSE, NULL, },
{"CSIBM866", CP866_Decode, CP866_Encode, FALSE, NULL, },
{"WINDOWS-874", CP874_Decode, CP874_Encode, FALSE, NULL, },
{"CP1250", CP1250_Decode, CP1250_Encode, FALSE, NULL, },
{"CP1251", CP1251_Decode, CP1251_Encode, FALSE, NULL, },
{"CP1252", CP1252_Decode, CP1252_Encode, FALSE, NULL, },
{"CP1253", CP1253_Decode, CP1253_Encode, FALSE, NULL, },
{"CP1254", CP1254_Decode, CP1254_Encode, FALSE, NULL, },
{"CP1255", CP1255_Decode, CP1255_Encode, FALSE, NULL, },
{"CP1256", CP1256_Decode, CP1256_Encode, FALSE, NULL, },
{"CP1257", CP1257_Decode, CP1257_Encode, FALSE, NULL, },
{"CP1258", CP1258_Decode, CP1258_Encode, FALSE, NULL, },
{"MS-EE", CP1250_Decode, CP1250_Encode, FALSE, NULL, },
{"MS-CYRL", CP1251_Decode, CP1251_Encode, FALSE, NULL, },
{"MS-ANSI", CP1252_Decode, CP1252_Encode, FALSE, NULL, },
{"MS-GREEK", CP1253_Decode, CP1253_Encode, FALSE, NULL, },
{"MS-TURK", CP1254_Decode, CP1254_Encode, FALSE, NULL, },
{"MS-HEBR", CP1255_Decode, CP1255_Encode, FALSE, NULL, },
{"MS-ARAB", CP1256_Decode, CP1256_Encode, FALSE, NULL, },
{"WINBALTRIM", CP1257_Decode, CP1257_Encode, FALSE, NULL, },
{"WINDOWS-1133", CP1133_Decode, CP1133_Encode, FALSE, NULL, },

/*Vietnamese*/
{"VISCII1", VISCII_Decode, VISCII_Encode, FALSE, NULL, },
{"CSVISCII", VISCII_Decode, VISCII_Encode, FALSE, NULL, },

/*Thai*/
{"TIS620", TIS620_Decode, TIS620_Encode, FALSE, NULL, },
{"TIS620-0", TIS620_Decode, TIS620_Encode, FALSE, NULL, },
{"TIS620.2529-1", TIS620_Decode, TIS620_Encode, FALSE, NULL, },
{"TIS620.2533-0", TIS620_Decode, TIS620_Encode, FALSE, NULL, },
{"TIS620.2533-1", TIS620_Decode, TIS620_Encode, FALSE, NULL, },
{"ISO-IR-166", TIS620_Decode, TIS620_Encode, FALSE, NULL, },

{"0", ASCII_Decode, ASCII_Encode, FALSE, NULL, },
{"1", UTF8_Decode, UTF8_Encode, FALSE, NULL, },
{"2", ISO8859_1_Decode, ISO8859_1_Encode, FALSE, NULL, },
{"3", ISO8859_2_Decode, ISO8859_2_Encode, FALSE, NULL, },
{"4", ISO8859_3_Decode, ISO8859_3_Encode, FALSE, NULL, },
{"5", ISO8859_4_Decode, ISO8859_4_Encode, FALSE, NULL, },
{"6", ISO8859_5_Decode, ISO8859_5_Encode, FALSE, NULL, },
{"7", ISO8859_6_Decode, ISO8859_6_Encode, FALSE, NULL, },
{"8", ISO8859_7_Decode, ISO8859_7_Encode, FALSE, NULL, },
{"9", ISO8859_8_Decode, ISO8859_8_Encode, FALSE, NULL, },
{"10", ISO8859_9_Decode, ISO8859_9_Encode, FALSE, NULL, },
{"11", ISO8859_10_Decode, ISO8859_10_Encode, FALSE, NULL, },
{"12", ISO8859_13_Decode, ISO8859_13_Encode, FALSE, NULL, },
{"13", ISO8859_15_Decode, ISO8859_15_Encode, FALSE, NULL, },
{"14", KOI8_R_Decode, KOI8_R_Encode, FALSE, NULL, },
{"15", KOI8_U_Decode, KOI8_U_Encode, FALSE, NULL, },
{"16", KOI8_RU_Decode, KOI8_RU_Encode, FALSE, NULL, },
{"17", TIS620_Decode, TIS620_Encode, FALSE, NULL, },
{"18", VISCII_Decode, VISCII_Encode, FALSE, NULL, },
{"19", BIG5_Decode, BIG5_Encode, FALSE, NULL, },
{"20", BIG5_Decode, BIG5_Encode, FALSE, NULL, },
{"21", GB2312_Decode, GB2312_Encode, FALSE, NULL, },
{"22", UTF8_Decode, UTF8_Encode, FALSE, NULL, },  /* Should be CN-GB */
{"23", UTF8_Decode, UTF8_Encode, FALSE, NULL, },  /* Should be CN-GB-12345 */
{"24", UTF8_Decode, UTF8_Encode, FALSE, NULL, },  /* Should be EUC-JP */
{"25", ISO2022_JP_Decode, ISO2022_JP_Encode, FALSE, NULL, },
{"26", UTF8_Decode, UTF8_Encode, FALSE, NULL, },  /* Should be ISO-2022-JP-1 */
{"27", UTF8_Decode, UTF8_Encode, FALSE, NULL, },  /* Should be ISO-2022-JP-2 */
{"28", ISO2022_CN_Decode, ISO2022_CN_Encode, FALSE, NULL, },
{"29", ISO2022_KR_Decode, ISO2022_KR_Encode, FALSE, NULL, },
{"30", EUC_KR_Decode, EUC_KR_Encode, FALSE, NULL, },
{"31", EUC_KR_Decode, EUC_KR_Encode, FALSE, NULL, },
{"32", EUC_KR_Decode, EUC_KR_Encode, FALSE, NULL, },
{"33", SJIS_Decode, SJIS_Encode, FALSE, NULL, },
{"34", SJIS_Decode, SJIS_Encode, FALSE, NULL, },

{NULL, NULL, NULL, FALSE, }
};

#if 0
#ifndef WIN32
void
SigHandler(int Signal)
{
	static int      Called=0;

	if (Called) {
		return;
	}

	Called=1;

	XplResumeThread(Tid);
}
#endif
#endif

StreamStruct
*GetStreamEncoder(unsigned char *Charset)
{
	StreamStruct        *Stream;
	long		        streamId;

	Stream = (StreamStruct *)MemMalloc(sizeof(StreamStruct) + sizeof(unsigned char) * (BUFSIZE));
	if (Stream) {
	    memset(Stream, 0, sizeof(StreamStruct));
	    Stream->Start = (unsigned char *)Stream + sizeof(StreamStruct);
	    Stream->End = Stream->Start + BUFSIZE;
        streamId = BongoKeywordFind(CodecIndex, Charset);
        if (streamId != -1) {
            if (StreamList[streamId].Encoder) {
                Stream->Codec = StreamList[streamId].Encoder;
                return(Stream);
            }
        }
	    MemFree(Stream);
	}
    return(NULL);
}

StreamStruct
*GetStreamDecoder(unsigned char *Charset)
{
	StreamStruct        *Stream;
	long		        streamId;

	Stream = (StreamStruct *)MemMalloc(sizeof(StreamStruct) + sizeof(unsigned char) * (BUFSIZE));
	if (Stream) {
	    memset(Stream, 0, sizeof(StreamStruct));
	    Stream->Start = (unsigned char *)Stream + sizeof(StreamStruct);
	    Stream->End = Stream->Start + BUFSIZE;
        streamId = BongoKeywordFind(CodecIndex, Charset);
        if (streamId != -1) {
            if (StreamList[streamId].Decoder) {
                Stream->Codec = StreamList[streamId].Decoder;
                return(Stream);
            }
        }
	    MemFree(Stream);
	}
    return(NULL);
}

StreamStruct
*GetStream(unsigned char *Charset, BOOL Encoder)
{
	StreamDescStruct	*StreamListPtr = StreamList;
	StreamStruct		*Stream;
	unsigned long		i=0;

	Stream=(StreamStruct *)MemMalloc(sizeof(StreamStruct)+sizeof(unsigned char)*(BUFSIZE));
	if (!Stream) {
		return(FALSE);
	}

	memset(Stream, 0, sizeof(StreamStruct));
	Stream->Start=(unsigned char *)Stream+sizeof(StreamStruct);
	Stream->End=Stream->Start+BUFSIZE;

	while (StreamListPtr[i].Charset!=NULL) {
		if (!XplStrCaseCmp(Charset, StreamListPtr[i].Charset)) {
			if (Encoder) {
				Stream->Codec=StreamListPtr[i].Encoder;
			} else {
				Stream->Codec=StreamListPtr[i].Decoder;
			}
			if (Stream->Codec==NULL) {
				MemFree(Stream);
				return(NULL);
			}
			return(Stream);
		}
		i++;
	}

	MemFree(Stream);
	return(NULL);
}

BOOL
ReleaseStream(StreamStruct *Stream)
{
	if (!Stream) {
		return(FALSE);
	}

	MemFree(Stream);
	return(TRUE);
}


StreamCodecFunc
FindCodec(unsigned char *Charset, BOOL Encoder)
{
	StreamDescStruct	*StreamListPtr = StreamList;
	unsigned long		i = 0;

	while (StreamListPtr[i].Charset!=NULL) {
		if (!XplStrCaseCmp(Charset, StreamListPtr[i].Charset)) {
			if (Encoder) {
				return(StreamListPtr[i].Encoder);
			} else {
				return(StreamListPtr[i].Decoder);
			}
		}
		i++;
	}

	return(NULL);
}

StreamCodecFunc
FindCodecEncoder(unsigned char *Charset)
{
    long codecId;
    
    codecId = BongoKeywordFind(CodecIndex, Charset);
    if (codecId != -1) {
        return(StreamList[codecId].Encoder);
    }

    return(NULL);
}

StreamCodecFunc
FindCodecDecoder(unsigned char *Charset)
{
    long codecId;
    
    codecId = BongoKeywordFind(CodecIndex, Charset);
    if (codecId != -1) {
        return(StreamList[codecId].Decoder);
    }

    return(NULL);
}

static BOOL
CodecSearchInit(void)
{
    BongoKeywordIndexCreateFromTable(CodecIndex, StreamList, .Charset, TRUE);

    if (CodecIndex) {
        return(TRUE);
    }

    return(FALSE);
}

static BOOL
CodecSearchShutdown(void)
{
    if (CodecIndex) {
        BongoKeywordIndexFree(CodecIndex);
        CodecIndex = NULL;
    }

    return(TRUE);
}


BOOL
StreamioInit(void)
{
    static BOOL initted = FALSE;
    
    if (!initted) {
	/* Warning - if streamio is ever used as a dynamically linked library */
	/* this function will need to be changed to only have the first caller do anything */
	if (!CodecSearchInit()) {
		return(FALSE);
	}

	if (!CodecHtmlInit()) {
        CodecSearchShutdown();
		return(FALSE);
	}
        initted = TRUE;
        
	return(TRUE);
    } else {
        return FALSE;
    }
}

BOOL
StreamioShutdown(void)
{
	/* Warning - if streamio is ever used as a dynamically linked library */
	/* this function will need to be changed to only have the last caller do anything */
	CodecHtmlShutdown();
    CodecSearchShutdown();
	return(TRUE);
}

