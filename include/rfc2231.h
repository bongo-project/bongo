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

#ifndef __RFC2231_H
#define __RFC2231_H
                                                                                                                                                                            
#define	RFC2231_PARAM_ALLOC_BLOCK_SIZE	12
#define	RFC2231_PARAM_LINE_LIMIT			48				/* Sanity check */

#include <xpl.h>

typedef struct {
	unsigned char	*Value;
	unsigned long 	ValueLen;
	BOOL				Encoded;
} RFC2231LineStruct;

typedef struct {
	unsigned long	LinesAllocated;
	unsigned long	Used;
} RFC2231ParamStruct;

__inline static void
rfc2231FreeParamLines(RFC2231ParamStruct *ParamLines)
{
	MemFree((void *)ParamLines);
}

__inline static BOOL
rcf2231SaveLine(unsigned char *paramValue, unsigned long paramLen, RFC2231ParamStruct **ParamLines, unsigned long LineNum, BOOL Encoded)
{
	RFC2231LineStruct	*line;

	if (ParamLines != NULL) {

		if (*ParamLines == NULL) {
			*ParamLines = MemMalloc(sizeof(RFC2231ParamStruct) + (sizeof(RFC2231LineStruct) * RFC2231_PARAM_ALLOC_BLOCK_SIZE));
			if (*ParamLines) {
				memset(*ParamLines, 0, sizeof(RFC2231ParamStruct) + (sizeof(RFC2231LineStruct) * RFC2231_PARAM_ALLOC_BLOCK_SIZE));
				(*ParamLines)->LinesAllocated	= RFC2231_PARAM_ALLOC_BLOCK_SIZE;
			} else {
				return(FALSE);
			}
		}

		if (LineNum < (*ParamLines)->LinesAllocated) {
			;
		} else {
			if (LineNum	< RFC2231_PARAM_LINE_LIMIT) {
				RFC2231ParamStruct *tmp;
				unsigned blocksNeeded = 0;

				do {
					blocksNeeded++;
				} while (((*ParamLines)->LinesAllocated + (blocksNeeded * RFC2231_PARAM_ALLOC_BLOCK_SIZE)) <= LineNum);

				tmp = MemRealloc(*ParamLines, sizeof(RFC2231ParamStruct) + (sizeof(RFC2231LineStruct) * ((RFC2231_PARAM_ALLOC_BLOCK_SIZE * blocksNeeded) + (*ParamLines)->LinesAllocated)));

				if (tmp) {
					*ParamLines = tmp;
					memset((unsigned char *)*ParamLines + sizeof(RFC2231ParamStruct) + (sizeof(RFC2231LineStruct) * (*ParamLines)->LinesAllocated), 0, sizeof(RFC2231LineStruct) * (RFC2231_PARAM_ALLOC_BLOCK_SIZE * blocksNeeded));
					(*ParamLines)->LinesAllocated += RFC2231_PARAM_ALLOC_BLOCK_SIZE;
				} else {
					return(FALSE);
				}
			} else {
				return(FALSE);
			}
		}

		line = (RFC2231LineStruct *)((unsigned char *)(*ParamLines) + sizeof(RFC2231ParamStruct));
		line[LineNum].Value = paramValue;
		line[LineNum].ValueLen = paramLen;
		line[LineNum].Encoded = Encoded;
		(*ParamLines)->Used++;
		return(TRUE);
	}

	return(FALSE);
}

__inline static BOOL
rfc2231JoinParamLines(unsigned char *ParamValue, unsigned long *ParamLen, RFC2231ParamStruct *ParamLines)
{

	if (ParamLines->Used > 0) {

		RFC2231LineStruct	*line	= (RFC2231LineStruct *)((unsigned char *)ParamLines + sizeof(RFC2231ParamStruct));
		unsigned char		*ptr	= ParamValue;		
		unsigned char		*endPtr	= ParamValue + *ParamLen;		
		unsigned long		i;
		BOOL					encodingStillLegal;		/* rfc2231 says that once you stop enconding, you can't start again */

		if (!line[0].Encoded) {
			for (i = 0; i < (ParamLines->Used); i++) {
				if ((ptr + line[i].ValueLen) < endPtr) {
					memcpy(ptr, line[i].Value, line[i].ValueLen);
					ptr += line[i].ValueLen;
					
				} else {
					*ParamValue = '\0';
					*ParamLen = 0;
					return(FALSE);
				}
			}
			*ptr = '\0';
			*ParamLen = ptr - ParamValue;
			return(TRUE);
		}

		if ((ptr + 3) < endPtr) {
			*ptr++ = '*';
			*ptr++ = '=';
			endPtr--;												/* Save room for trailing ';' */
			encodingStillLegal = TRUE;

			for (i = 0; i < (ParamLines->Used); i++) {
				if (encodingStillLegal && (line[i].Encoded == FALSE)) {
					encodingStillLegal = FALSE;
					*ptr++ = ';';									/* This tells parser that the encoded portion of the string ends here */
					endPtr++;
				} 

				if ((ptr + line[i].ValueLen) < endPtr) {
					memcpy(ptr, line[i].Value, line[i].ValueLen);
					ptr += line[i].ValueLen;
					
				} else {
					*ParamValue = '\0';
					*ParamLen = 0;
					return(FALSE);
				}
			}

			*ptr = '\0';
			*ParamLen = ptr - ParamValue;
			return(TRUE);
		}
	}
	return(FALSE);
}

__inline static unsigned char *
GetParamValue(unsigned char *Data, unsigned char **Value, unsigned long *ValueLen)
{
	unsigned char *end;

	if ((*Data == '"') && (end = strchr(Data + 1, '"'))) {
		Data++;
		*ValueLen	= end - Data;
		*Value		= Data;

		return(Data + *ValueLen + 1);
	}

	if (*Data != '"') {
		end = Data;
	} else {
		Data++;
		end = Data;
	}
	
	while (*end && !isspace(*end) && (*end != ';') && (*end != '"')) {
		end++;
	}

	*ValueLen	= end - Data;
	*Value		= Data;

	return(end);
}


__inline static unsigned char *
ParseParam(unsigned char *Data, unsigned char *Buffer, unsigned long *BuffLen, RFC2231ParamStruct **RFC2231ParamLines)
{
	unsigned char *nextChar;
	unsigned char *paramValue;
	unsigned long paramLen;

	if (Data[0] == '=') {
		nextChar = GetParamValue(Data + 1, &paramValue, &paramLen);
		if (paramLen < *BuffLen) {
			*BuffLen = paramLen;
		}

		memcpy(Buffer, paramValue, *BuffLen);
		Buffer[*BuffLen] = '\0';

		return (nextChar);
	}
	
	if (Data[0] == '*' && RFC2231ParamLines) {
		/* We have an rfc2231 parameter */
		BOOL				encoded;

		if (Data[1] == '=') {
			encoded = TRUE;
			nextChar = GetParamValue(Data + 2, &paramValue, &paramLen);
			rcf2231SaveLine(paramValue, paramLen, RFC2231ParamLines, 0, encoded);

			return (nextChar);
		}
		
		if (isdigit(Data[1])) {
			/* We have a multi-lined rfc2231 parameter */
			unsigned char	*ptr;

			ptr = Data + 1; 

			do {
				ptr++;
			} while (isdigit(*ptr));

			if (*ptr == '*') {
				encoded = TRUE;
				ptr++;															
			} else {
				encoded = FALSE;
			}

			if (*ptr == '=') {
				ptr++;
				nextChar = GetParamValue(ptr, &paramValue, &paramLen);
				rcf2231SaveLine(paramValue, paramLen, RFC2231ParamLines, atol(Data + 1), encoded);

				return(nextChar);
			} 

			return(ptr);
		}
	}
	return(Data);
}
#endif /* __RFC2231_H */

