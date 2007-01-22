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

EXPORT const unsigned char quotableText[] = {
/*	0x00	000	NUL	Null								*/	0, 
/*	0x01	001	SOH	Start of Heading				*/	0,
/*	0x02	002	STX	Start Text						*/	0,
/*	0x03	003	ETX	End Text							*/	0,
/*	0x04	004	EOT	End of Transmisison			*/	0,
/*	0x05	005	ENQ	Enquiry							*/	0,
/*	0x06	006	ACK	Acknowledge						*/	0,
/*	0x07	007	BEL	Bell								*/	0,
/*	0x08	008	BS		Backspace						*/	0,
/*	0x09	009	HT		Horizontal Tab					*/	1,
/*	0x0A	010	LF		Linefeed							*/	0,
/*	0x0B	011	VT		Vertical Tab					*/	0,
/*	0x0C	012	FF		Formfeed							*/	0,
/*	0x0D	013	CR		Carriage Return				*/	0,
/*	0x0E	014	SO		Shift Out						*/	0,
/*	0x0F	015	SI		Shift In							*/	0,
/*	0x10	016	DLE	Data Link Escape				*/	0,
/*	0x11	017	DC1	Device Control 1				*/	0,
/*	0x12	018	DC2	Device Control 2				*/	0,
/*	0x13	019	DC3	Device Control 3				*/	0,
/*	0x14	020	DC4	Device Control 4				*/	0,
/*	0x15	021	NAK	Negative Acknowledgement	*/	0,
/*	0x16	022	SYN	Synchronous Idle				*/	0,
/*	0x17	023	ETB	End Transmission Block		*/	0,
/*	0x18	024	CAN	Cancel							*/	0,
/*	0x19	025	EM		End Medium						*/	0,
/*	0x1A	026	EOF	End of File						*/	0,
/*	0x1B	027	ESC	Escape							*/	0,
/*	0x1C	028	FS		File Separator					*/	0,
/*	0x1D	029	GS		Group Separator				*/	0,
/*	0x1E	030	HOM	Home								*/	0,
/*	0x1F	031	NEW	Newline							*/	0,
/*	0x20	032	SPA	Space								*/	1,
/*	0x21	033	!		Exclamation Point				*/	1,
/*	0x22	034	"		Double Quote					*/	0,
/*	0x23	035	#		Pound Sign						*/	1,
/*	0x24	036	$		Dollar Sign						*/	1,
/*	0x25	037	%		Percent							*/	1,
/*	0x26	038	&		Ampersand						*/	1,
/*	0x27	039	'		Apostrophe						*/	1,
/*	0x28	040	(		Right Parenthesis				*/	1,
/*	0x29	041	)		Left Parenthesis				*/	1,
/*	0x2A	042	*		Asterick							*/	1,
/*	0x2B	043	+		Plus								*/	1,
/*	0x2C	044	,		Comma								*/	1,
/*	0x2D	045	-		Minus								*/	1,
/*	0x2E	046	.		Period							*/	1,
/*	0x2F	047	/		Forward Slash					*/	1,
/*	0x30	048	0		Zero								*/	1,
/*	0x31	049	1		One								*/	1,
/*	0x32	050	2		Two								*/	1,
/*	0x33	051	3		Three								*/	1,
/*	0x34	052	4		Four								*/	1,
/*	0x35	053	5		Five								*/	1,
/*	0x36	054	6		Six								*/	1,
/*	0x37	055	7		Seven								*/	1,
/*	0x38	056	8		Eight								*/	1,
/*	0x39	057	9		Nine								*/	1,
/*	0x3A	058	:		Colon								*/	1,
/*	0x3B	059	;		Semi-Colon						*/	1,
/*	0x3C	060	<		Less-than						*/	1,
/*	0x3D	061	=		Equal								*/	1,
/*	0x3E	062	>		Greater-than					*/	1,
/*	0x3F	063	?		Question Mark					*/	1,
/*	0x40	064	@		At									*/	1,
/*	0x41	065	A		Uppercase A						*/	1,
/*	0x42	066	B		Uppercase B						*/	1,
/*	0x43	067	C		Uppercase C						*/	1,
/*	0x44	068	D		Uppercase D						*/	1,
/*	0x45	069	E		Uppercase E						*/	1,
/*	0x46	070	F		Uppercase F						*/	1,
/*	0x47	071	G		Uppercase G						*/	1,
/*	0x48	072	H		Uppercase H						*/	1,
/*	0x49	073	I		Uppercase I						*/	1,
/*	0x4A	074	J		Uppercase J						*/	1,
/*	0x4B	075	K		Uppercase K						*/	1,
/*	0x4C	076	L		Uppercase L						*/	1,
/*	0x4D	077	M		Uppercase M						*/	1,
/*	0x4E	078	N		Uppercase N						*/	1,
/*	0x4F	079	O		Uppercase O						*/	1,
/*	0x50	080	P		Uppercase P						*/	1,
/*	0x51	081	Q		Uppercase Q						*/	1,
/*	0x52	082	R		Uppercase R						*/	1,
/*	0x53	083	S		Uppercase S						*/	1,
/*	0x54	084	T		Uppercase T						*/	1,
/*	0x55	085	U		Uppercase U						*/	1,
/*	0x56	086	V		Uppercase V						*/	1,
/*	0x57	087	W		Uppercase W						*/	1,
/*	0x58	088	X		Uppercase X						*/	1,
/*	0x59	089	Y		Uppercase Y						*/	1,
/*	0x5A	090	Z		Uppercase Z						*/	1,
/*	0x5B	091	[											*/	1,
/*	0x5C	092	\		Back Slash						*/	0,
/*	0x5D	093	]											*/	1,
/*	0x5E	094	^											*/	1,
/*	0x5F	095	_		Underscore						*/	1,
/*	0x60	096	`											*/	1,
/*	0x61	097	a		Lowercase a						*/	1,
/*	0x62	098	b		Lowercase b						*/	1,
/*	0x63	099	c		Lowercase c						*/	1,
/*	0x64	100	d		Lowercase d						*/	1,
/*	0x65	101	e		Lowercase e						*/	1,
/*	0x66	102	f		Lowercase f						*/	1,
/*	0x67	103	g		Lowercase g						*/	1,
/*	0x68	104	h		Lowercase h						*/	1,
/*	0x69	105	i		Lowercase i						*/	1,
/*	0x6A	106	j		Lowercase j						*/	1,
/*	0x6B	107	k		Lowercase k						*/	1,
/*	0x6C	108	l		Lowercase l						*/	1,
/*	0x6D	109	m		Lowercase m						*/	1,
/*	0x6E	110	n		Lowercase n						*/	1,
/*	0x6F	111	o		Lowercase o						*/	1,
/*	0x70	112	p		Lowercase p						*/	1,
/*	0x71	113	q		Lowercase q						*/	1,
/*	0x72	114	r		Lowercase r						*/	1,
/*	0x73	115	s		Lowercase s						*/	1,
/*	0x74	116	t		Lowercase t						*/	1,
/*	0x75	117	u		Lowercase u						*/	1,
/*	0x76	118	v		Lowercase v						*/	1,
/*	0x77	119	w		Lowercase w						*/	1,
/*	0x78	120	x		Lowercase x						*/	1,
/*	0x79	121	y		Lowercase y						*/	1,
/*	0x7A	122	z		Lowercase z						*/	1,
/*	0x7B	123	{											*/	1,
/*	0x7C	124	|											*/	1,
/*	0x7D	125	}											*/	1,
/*	0x7E	126	~											*/	1,
/*	0x7F	127	DEL										*/	0,
/*	0x80	128												*/	0,
/*	0x81	129												*/	0,
/*	0x82	130												*/	0,
/*	0x83	131												*/	0,
/*	0x84	132												*/	0, 
/*	0x85	133												*/	0, 
/*	0x86	134												*/	0, 
/*	0x87	135												*/	0, 
/*	0x88	136												*/	0, 
/*	0x89	137												*/	0, 
/*	0x8A	138												*/	0, 
/*	0x8B	139												*/	0, 
/*	0x8C	140												*/	0, 
/*	0x8D	141												*/	0, 
/*	0x8E	142												*/	0, 
/*	0x8F	143												*/	0, 
/*	0x90	144												*/	0, 
/*	0x91	145												*/	0, 
/*	0x92	146												*/	0, 
/*	0x93	147												*/	0, 
/*	0x94	148												*/	0, 
/*	0x95	149												*/	0, 
/*	0x96	150												*/	0, 
/*	0x97	151												*/	0, 
/*	0x98	152												*/	0, 
/*	0x99	153												*/	0, 
/*	0x9A	154												*/	0, 
/*	0x9B	155												*/	0, 
/*	0x9C	156												*/	0, 
/*	0x9D	157												*/	0, 
/*	0x9E	158												*/	0, 
/*	0x9F	159												*/	0, 
/*	0xA0	160												*/	0, 
/*	0xA1	161												*/	0, 
/*	0xA2	162												*/	0, 
/*	0xA3	163												*/	0, 
/*	0xA4	164												*/	0, 
/*	0xA5	165												*/	0, 
/*	0xA6	166												*/	0, 
/*	0xA7	167												*/	0, 
/*	0xA8	168												*/	0, 
/*	0xA9	169												*/	0, 
/*	0xAA	170												*/	0, 
/*	0xAB	171												*/	0, 
/*	0xAC	172												*/	0, 
/*	0xAD	173												*/	0, 
/*	0xAE	174												*/	0, 
/*	0xAF	175												*/	0, 
/*	0xB0	176												*/	0, 
/*	0xB1	177												*/	0, 
/*	0xB2	178												*/	0, 
/*	0xB3	179												*/	0, 
/*	0xB4	180												*/	0, 
/*	0xB5	181												*/	0, 
/*	0xB6	182												*/	0, 
/*	0xB7	183												*/	0, 
/*	0xB8	184												*/	0, 
/*	0xB9	185												*/	0, 
/*	0xBA	186												*/	0, 
/*	0xBB	187												*/	0, 
/*	0xBC	188												*/	0, 
/*	0xBD	189												*/	0, 
/*	0xBE	190												*/	0, 
/*	0xBF	191												*/	0, 
/*	0xC0	192												*/	0, 
/*	0xC1	193												*/	0, 
/*	0xC2	194												*/	0, 
/*	0xC3	195												*/	0, 
/*	0xC4	196												*/	0, 
/*	0xC5	197												*/	0, 
/*	0xC6	198												*/	0, 
/*	0xC7	199												*/	0, 
/*	0xC8	200												*/	0, 
/*	0xC9	201												*/	0, 
/*	0xCA	202												*/	0, 
/*	0xCB	203												*/	0, 
/*	0xCC	204												*/	0, 
/*	0xCD	205												*/	0, 
/*	0xCE	206												*/	0, 
/*	0xCF	207												*/	0, 
/*	0xD0	208												*/	0, 
/*	0xD1	209												*/	0, 
/*	0xD2	210												*/	0, 
/*	0xD3	211												*/	0, 
/*	0xD4	212												*/	0, 
/*	0xD5	213												*/	0, 
/*	0xD6	214												*/	0, 
/*	0xD7	215												*/	0, 
/*	0xD8	216												*/	0, 
/*	0xD9	217												*/	0, 
/*	0xDA	218												*/	0, 
/*	0xDB	219												*/	0, 
/*	0xDC	220												*/	0, 
/*	0xDD	221												*/	0, 
/*	0xDE	222												*/	0, 
/*	0xDF	223												*/	0, 
/*	0xE0	224												*/	0, 
/*	0xE1	225												*/	0, 
/*	0xE2	226												*/	0, 
/*	0xE3	227												*/	0, 
/*	0xE4	228												*/	0, 
/*	0xE5	229												*/	0, 
/*	0xE6	230												*/	0, 
/*	0xE7	231												*/	0, 
/*	0xE8	232												*/	0, 
/*	0xE9	233												*/	0, 
/*	0xEA	234												*/	0, 
/*	0xEB	235												*/	0, 
/*	0xEC	236												*/	0, 
/*	0xED	237												*/	0, 
/*	0xEE	238												*/	0, 
/*	0xEF	239												*/	0, 
/*	0xF0	240												*/	0, 
/*	0xF1	241												*/	0, 
/*	0xF2	242												*/	0, 
/*	0xF3	243												*/	0, 
/*	0xF4	244												*/	0, 
/*	0xF5	245												*/	0, 
/*	0xF6	246												*/	0, 
/*	0xF7	247												*/	0, 
/*	0xF8	248												*/	0, 
/*	0xF9	249												*/	0, 
/*	0xFA	250												*/	0, 
/*	0xFB	251												*/	0, 
/*	0xFC	252												*/	0, 
/*	0xFD	253												*/	0, 
/*	0xFE	254												*/	0, 
/*	0xFF	255												*/	0
};

/* Quoted_String_Decode */
/* This stream makes an ascii string out of a quoted string */
/* This needs tested */
static BOOL
Quoted_String_Decode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	*In=Codec->Start;
	unsigned char	*Out=NextCodec->Start+NextCodec->Len;
	unsigned char	*OutEnd=NextCodec->End;
	unsigned long	Len=0;

	while (Len < Codec->Len) {
		if (*In != '"') {
			FlushOutStream(1);
			*Out = *In;
			Out++;
			In++;
			NextCodec->Len++;
			Len++;
		} else {
			/* make sure there is a closing quote in the stream */
			unsigned char *endPtr;
			unsigned char *ptr;

			endPtr = In + (Codec->Len - Len);
			ptr = In;
			while (ptr < endPtr) {
				if ((*ptr != '"') || (*(ptr - 1) == '\\')) {
					ptr++;
					continue;
				}
			}

			if (ptr < endPtr) {
				/* We have the whole quoted string */
				while (Len < Codec->Len) {
					if ((*In != '\\') && (*In != '"')) {
						FlushOutStream(1);
						*Out = *In;
						Out++;
						In++;
						NextCodec->Len++;
						Len++;
					} else {
						if (*In == '"') {
							/* skip closing quote and stop decoding */
							In++;
							Len++;
							break;
						} else {
							/* we have a quoted pair */
							FlushOutStream(2);

							/* skip the slash */
							In++;
							Len++;

							/* pass the escaped char through */
							*Out=*In;
							Out++;
							In++;
							NextCodec->Len++;
							Len++;
						}
					}
				}
			} else {
				unsigned long count;

				if (!Codec->EOS) {
					/* Go get more */
				    if (Len >= Codec->Min) {
					return(Len);
				    }

				    return(Codec->Len);
				}

				/* This is not a quoted string */
				count = endPtr - In;
				do {
					if (count < BUFSIZE) {
						FlushOutStream(count);
						memcpy(Out, In, count);
						Out += count;
						In += count;
						NextCodec->Len += count;
						Len += count;
						count = 0;
						break;
					} 
					FlushOutStream(BUFSIZE);
					memcpy(Out, In, BUFSIZE);
					Out += BUFSIZE;
					In += BUFSIZE;
					NextCodec->Len += BUFSIZE;
					Len += BUFSIZE;
					count -= BUFSIZE;
				} while (count > 0);
			}
		}
	}


	EndFlushStream;

	return(Len);
}

/* Quoted_String_Encode */
/* This stream makes a quoted string out of a ascii string */
/* The entire stream is require */

static int
Quoted_String_Encode(StreamStruct *Codec, StreamStruct *NextCodec)
{
    unsigned long	Len=0;

    if (Codec->EOS) {
	unsigned char	*In = Codec->Start;
	unsigned char	*Out = NextCodec->Start + NextCodec->Len;
	unsigned char	*OutEnd = NextCodec->End;

	unsigned long	wordLimit;
	unsigned long	outLen;

	FlushOutStream(1);
	*Out = '"';
	Out++;
	NextCodec->Len++;

	wordLimit	= (unsigned long)(Codec->StreamData);

	if (wordLimit > 0) {
	    outLen = 2;

	    while (Len < Codec->Len) {
		if (quotableText[*In]) {
		    outLen++;
		    if (outLen < wordLimit) {
			FlushOutStream(1);
			*Out = (unsigned char)*In;
			Out++;
			In++;
			NextCodec->Len++;
			Len++;
			continue;
		    }
		    break;
		} else if ((*In == '\\') || (*In == '"')) {
		    outLen += 2;
		    if (outLen < wordLimit) {
			FlushOutStream(2);
			*Out = '\\';
			Out++;
			NextCodec->Len++;

			*Out = (unsigned char)*In;
			Out++;
			In++;
			NextCodec->Len++;
			Len++;
			continue;
		    }

		    break;
		} else {
		    outLen++;
		    if (outLen < wordLimit) {
			FlushOutStream(1);
			*Out = '^';
			Out++;
			In++;
			NextCodec->Len++;
			Len++;
			continue;
		    }
		    break;
		}
	    }
	} else {
	    while (Len < Codec->Len) {
		if (quotableText[*In]) {
		    FlushOutStream(1);
		    *Out = (unsigned char)*In;
		    Out++;
		    In++;
		    NextCodec->Len++;
		    Len++;
		    continue;
		} else if ((*In == '\\') || (*In == '"'))	{
		    FlushOutStream(2);
		    *Out = '\\';
		    Out++;
		    NextCodec->Len++;

		    *Out = (unsigned char)*In;
		    Out++;
		    In++;
		    NextCodec->Len++;
		    Len++;
		    continue;
		} else {
		    FlushOutStream(1);
		    *Out = '^';
		    Out++;
		    In++;
		    NextCodec->Len++;
		    Len++;
		    continue;
		}
	    }
	}

	FlushOutStream(1);
	*Out = '"';
	Out++;
	NextCodec->Len++;

	EndFlushStream;

	return(Len);
    }

    if (Len >= Codec->Min) {
	return(Len);
    }

    return(Codec->Len);
}
