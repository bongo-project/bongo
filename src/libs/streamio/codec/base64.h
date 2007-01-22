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

/* Base64 encoding/decoding tables */

static unsigned char BASE64[] = {"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"};

static unsigned char BASE64_R[256] = {
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x3E, 0xFF, 0xFF, 0xFF, 0x3F,
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

static int
BASE64_Decode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	*In=Codec->Start;
	unsigned char	*Out=NextCodec->Start+NextCodec->Len;
	unsigned char	*OutEnd=NextCodec->End;
	unsigned long	Len=0;
	unsigned char	Chunk[4];
	long				i;

	while (Len<Codec->Len) {
		FlushOutStream(6);

		/* Grab a chunk */
		i=0;
		while ((i<4) && (Len<Codec->Len)) {
			if ((In[0]!='\r') && (In[0]!='\n')) {
				Chunk[i++]=In[0];
			}
			In++;
			Len++;
		}

		if (i<4) {
			/* Not enough data for a whole chunk */
			if (Codec->EOS) {
				if (Len==Codec->Len) {
					goto BASE64DecodeDone;	/* Must have had CR/LF at the end */
				}
				/* Oops, someone screwed up the data */
				for (; i<4; i++) {
					/* do some padding */
					Chunk[i]='=';
				}
			} else {
				/* We already pulled the chunk, we need to put it back into the buffer now :-( */
				for (; i>0; i--) {
					In--;
					Len--;
				}
				
				if (Len >= Codec->Min) {
				    return(Len);
				}

				return(Codec->Len);
			}
		}


		Out[0]=((BASE64_R[Chunk[0]] << 2) & 0xFC) | ((BASE64_R[Chunk[1]] >> 4) & 0x03);
		NextCodec->Len++;
		Out++;

		Out[0]=((BASE64_R[Chunk[1]] << 4) & 0xF0) | ((BASE64_R[Chunk[2]] >> 2) & 0x0F);
		NextCodec->Len++;
		Out++;

		Out[0]=((BASE64_R[Chunk[2]] << 6) & 0xC0) | ((BASE64_R[Chunk[3]])      & 0x3F);
		Out++;
		NextCodec->Len++;

		if (Chunk[3]=='=') {
			/* One padding byte */
			Out--;
			NextCodec->Len--;
			if (Chunk[2]=='=') {
				/* Two padding bytes */
				Out--;
				NextCodec->Len--;
			}
		}
	}

BASE64DecodeDone:
	EndFlushStream;

        if (Len >= Codec->Min) {
            return(Len);
        }

        return(Codec->Len);
}


static int
BASE64_Encode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	*In=Codec->Start;
	unsigned char	*Out=NextCodec->Start+NextCodec->Len;
	unsigned char	*OutEnd=NextCodec->End;
	unsigned long	Len=0;


	while (Len<Codec->Len) {
		FlushOutStream(6);

		/* Make sure we've got enough data to work on */
		if ((Codec->Len-Len)>2) {
			Out[0] = BASE64[((In[0] >> 2) & 0x3F)];
			NextCodec->Len++;
			Out++;

			Out[0] = BASE64[((In[0] << 4) & 0x30) | ((In[1] >> 4) & 0x0F)];
			NextCodec->Len++;
			Out++;

			Out[0] = BASE64[((In[1] << 2) & 0x3C) | ((In[2] >> 6) & 0x03)];
			NextCodec->Len++;
			Out++;

			Out[0] = BASE64[((In[2] >> 0) & 0x3F)];
			NextCodec->Len++;
			Out++;

			Len+=3;
			In+=3;
			Codec->State++;
		} else {
			if (Codec->EOS) {
				Out[0] = BASE64[(In[0] >> 2) & 0x3F];
				Out[1] = BASE64[(In[0] << 4) & 0x30];
				if ((Codec->Len-Len) == 2) {
//					Out[1] |= BASE64[(In[1] >> 4) & 0x0F];
					Out[1]  = BASE64[((In[0] << 4) & 0x30) | ((In[1] >> 4) & 0x0F)];
					Out[2]  = BASE64[(In[1] << 2) & 0x3C];
				} else {
					Out[2] = '=';
				}
				Out[3] = '=';
				Out+=4;
				NextCodec->Len+=4;
				Len=Codec->Len;
				Codec->State++;
			} else {
				/* We want more data, just return and wait to be called back */
			    if (Len >= Codec->Min) {
				return(Len);
			    }

			    return(Codec->Len);
			}
		}

		/* Should we send a line break? */
		if ((Codec->State % 19)==0) {
			Out[0]='\r';
			Out++;
			Out[0]='\n';
			Out++;
			NextCodec->Len+=2;
			Codec->State=0;
		}
	}

	if ((NextCodec->Len > 0) || (Codec->EOS)) {
	    unsigned long bytesProcessed;

	    if ((Len == Codec->Len) && (Codec->EOS)) {
		/* Terminate the stream nicely with a CR/LF */
		if (Codec->EOS && ((Codec->State % 19) != 0)) {
		    Out[0] = '\r';
		    Out++;
		    Out[0] = '\n';
		    Out++;
		    NextCodec->Len += 2;
		    Codec->State = 0;
		}
		NextCodec->EOS = TRUE;
	    }

	    bytesProcessed = NextCodec->Codec(NextCodec, NextCodec->Next); 
	    if (bytesProcessed >= NextCodec->Len) {
		NextCodec->Len = 0;
	    } else {
		NextCodec->Len -= bytesProcessed;
		memmove(NextCodec->Start, Out-NextCodec->Len, NextCodec->Len);
	    }
	}

        if (Len >= Codec->Min) {
            return(Len);
        }

        return(Codec->Len);
}
