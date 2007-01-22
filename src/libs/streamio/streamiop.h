
#define	_STREAMIO_INTERNAL_
#define	UNKNOWN_CHAR	'?'

/***************************************************************************
 ALL INTERNAL MACROS REQUIRED TO GET THE TABLE AND .H FILES GOING
***************************************************************************/

#define	UTF8EncodeChar(unichar, dest, count, len)									\
		if ((unichar) < 0x0080) {								/* 0000-007F */		\
			(dest)[0] = (unsigned char)((unichar));									\
			count++;																				\
			len++;																				\
		} else if ((unichar) < 0x0800) {					/* 0080-07FF */			\
			(dest)[0] = (unsigned char)(0xC0 | (((unichar) & 0x07C0) >> 6));	\
			(dest)[1] = (unsigned char)(0x80 | ((unichar) & 0x003F));			\
			count+=2;																			\
			len+=2;																				\
		} else {													/* 0800-FFFF */			\
			(dest)[0] = (unsigned char)(0xE0 | (((unichar) & 0xF000) >> 12));	\
			(dest)[1] = (unsigned char)(0x80 | (((unichar) & 0x0FC0) >> 6));	\
			(dest)[2] = (unsigned char)(0x80 | ((unichar) & 0x003F));			\
			count+=3;																			\
			len+=3;																				\
		}


/* This function only handles UCS-2 */
#define	UTF8DecodeChar(src, uchar, len, limit)																			\
{																																		\
	if (src[0] <= 0x7F) {					/* 0000-007F: one byte (0xxxxxxx) */									\
		uchar = (unicode)src[0];																								\
		len++;																														\
		src++;																														\
	} else if (src[0] <= 0xDF) {			/* 0080-07FF: two bytes (110xxxxx 10xxxxxx) */						\
		if (limit > (len + 1)) {			/* make sure we have enough bytes to do the operation */			\
			uchar = ((((unicode)src[0]) & 0x001F) << 6) |																\
					  ((((unicode)src[1]) & 0x003F) << 0);																	\
			len+=2;																													\
			src+=2;																													\
		} else {																														\
			uchar = (unicode)src[0];																							\
			len++;																													\
			src++;																													\
		}																																\
	} else {										/* 0800-FFFF: three bytes (1110xxxx 10xxxxxx 10xxxxxx) */		\
		if (limit > (len + 2)) {			/* make sure we have enough bytes to do the operation */			\
			uchar = ((((unicode)src[0]) & 0x000F) << 12) |																\
					  ((((unicode)src[1]) & 0x003F) << 6) |																\
					  ((((unicode)src[2]) & 0x003F) << 0);																	\
			len+=3;																													\
			src+=3;																													\
		} else {																														\
			uchar = (unicode)src[0];																							\
			len++;																													\
			src++;																													\
		}																																\
	}																																	\
}

__inline static BOOL
FlushOutStreamEx(StreamStruct *Codec, StreamStruct *NextCodec, unsigned char **Out, unsigned char *OutEnd, unsigned long Space)
{
    if ((*Out + Space) < OutEnd) {
	return(TRUE);
    }

    if (NextCodec->Len >= Space) {
	unsigned long bytesProcessed;

	bytesProcessed = NextCodec->Codec(NextCodec, NextCodec->Next);
	if (bytesProcessed == NextCodec->Len) {
	    NextCodec->Len = 0;
	   *Out = NextCodec->Start;
	    return(TRUE);
	}

	if (bytesProcessed < NextCodec->Len) {
	    if (bytesProcessed != 0) {
		NextCodec->Len -= bytesProcessed;
		memmove(NextCodec->Start, *Out - NextCodec->Len, NextCodec->Len);
		*Out = NextCodec->Start + NextCodec->Len;

		if ((*Out + Space) < OutEnd) {
		    return(TRUE);
		}
	    }
		
	    do {
		NextCodec->Min = Space + 1 - (OutEnd - *Out); /* The codec can't return 0 unless Min bytes are free in its buffer */
		bytesProcessed = NextCodec->Codec(NextCodec, NextCodec->Next);

		if (bytesProcessed == NextCodec->Len) {
		    NextCodec->Min = 0;
		    NextCodec->Len = 0;
		    *Out = NextCodec->Start;
		    return (TRUE);
		} 

		if (bytesProcessed == 0) {
		    XplConsolePrintf("\rSTREAMIO: The stream returned 0 when it was required to return at least %lu\n", NextCodec->Min);
		    NextCodec->Min = 0;
		    return(FALSE);
		}

		NextCodec->Min = 0;

		if (bytesProcessed < NextCodec->Len) {
		    NextCodec->Len -= bytesProcessed;
		    memmove(NextCodec->Start, *Out - NextCodec->Len, NextCodec->Len);
		    *Out = NextCodec->Start + NextCodec->Len;
		    continue;
		}

		XplConsolePrintf("\rSTREAMIO: The next stream claims to have process more data than was in its buffer!\n");
		NextCodec->Len = 0;
		*Out = NextCodec->Start;
		return(TRUE);
	    } while ((*Out + Space) >= OutEnd);
		
	    return(TRUE);
	}

	XplConsolePrintf("\rSTREAMIO: The next stream claims to have process more data than was in its buffer!\n");
	NextCodec->Len = 0;
	*Out = NextCodec->Start;
	    
	return(TRUE);
    }

    XplConsolePrintf("\rSTREAMIO: The current stream is asking for more space than the next codec has in its entire buffer!\n");
    return(FALSE);
}

#define FlushOutStream(Space) FlushOutStreamEx(Codec, NextCodec, &Out, OutEnd, Space)

__inline static BOOL
EndFlushStreamEx(StreamStruct *Codec, StreamStruct *NextCodec, unsigned char *Out, unsigned long Len)
{
    unsigned long bytesProcessed;

    if ((Codec->EOS) && (Len == Codec->Len)) {
	if (Len == Codec->Len) {
	    /* The current codec has processed EOS */
	    NextCodec->EOS = TRUE;

	    bytesProcessed = NextCodec->Codec(NextCodec, NextCodec->Next);
	    if (bytesProcessed == NextCodec->Len) {
		NextCodec->Len = 0;
		return(TRUE);
	    }

	    if (bytesProcessed < NextCodec->Len) {
		XplConsolePrintf("\rSTREAMIO: The next stream has EOS. It should not return unless its entire buffer is processed\n");
		NextCodec->Len -= bytesProcessed;
		return(FALSE);
	    }

	    XplConsolePrintf("\rSTREAMIO: The next stream claims to have process more data than was in its buffer!\n");
	    NextCodec->Len = 0;

	    return(TRUE);
	}
    }

    if (NextCodec->Len > 0) {
	bytesProcessed = NextCodec->Codec(NextCodec, NextCodec->Next);
	if (bytesProcessed == NextCodec->Len) {
	    NextCodec->Len = 0;
	    return(TRUE);
	}

	if (bytesProcessed < NextCodec->Len)  {
	    NextCodec->Len -= bytesProcessed;
	    memmove(NextCodec->Start, Out - NextCodec->Len, NextCodec->Len);
	    return(TRUE);
	}

	XplConsolePrintf("\rSTREAMIO: The next stream claims to have process more data than was in its buffer!\n");
	NextCodec->Len = 0;

	return(TRUE);
    }

    return(TRUE);
}

#define EndFlushStream EndFlushStreamEx(Codec, NextCodec, Out, Len)

__inline static long
FlushOutBlockEx(StreamStruct *Codec, StreamStruct *NextCodec, unsigned char *In, unsigned char *Out, unsigned char *OutEnd, long Size)
{
    long outSpace;
    long count = Size;

    do {
	outSpace = OutEnd - Out;
	if (outSpace > count) {
	    memcpy(Out, In, count);
	    Out += count;
	    NextCodec->Len += count;
	    In += count;
	} else {
	    memcpy(Out, In, outSpace);
	    Out += outSpace;
	    NextCodec->Len += outSpace;
	    In += outSpace;
	    count -= outSpace;
	}
	FlushOutStreamEx(Codec, NextCodec, &Out, OutEnd, 5);
    } while (count > 0);
    return(Size);
}

#define FlushOutBlock(IN, OUT, SIZE) FlushOutBlockEx(Codec, NextCodec, IN, OUT, OutEnd, SIZE)

#define	CheckUTFInStream \
if (((Len+3)>=Codec->Len) && !Codec->EOS) { \
    if (Len >= Codec->Min) { \
	return(Len); \
    } \
    return(Codec->Len); \
}

#define	CheckInStream(Space) \
if ((Len + Space) > Codec->Len) { \
    if (!Codec->EOS) { \
        if (Len >= Codec->Min) { \
            return(Len); \
        } \
        return(Codec->Len); \
    } else { \
	Len = Codec->Len; \
	EndFlushStream; \
	return(Len); \
    } \
}

typedef struct {
	unsigned short	indx;
	unsigned short	used;
} Summary16;

#define	BUFSIZE			1023

typedef unsigned long	ucs4_t;
typedef unsigned long	state_t;
typedef struct _conv_t	*conv_t;

StreamStruct		*GetStream(unsigned char *Charset, BOOL Encoder);
BOOL					ReleaseStream(StreamStruct *Stream);
StreamCodecFunc	FindCodec(unsigned char *Charset, BOOL Encoder);
