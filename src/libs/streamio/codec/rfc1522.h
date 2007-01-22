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

#define MAX_ENCODED_WORD_LEN	500

EXPORT const unsigned char Token[] = {
/*	0x00	000	NUL	Null								*/ 0, 
/*	0x01	001	SOH	Start of Heading				*/ 0,
/*	0x02	002	STX	Start Text						*/ 0,
/*	0x03	003	ETX	End Text							*/	0,
/*	0x04	004	EOT	End of Transmisison			*/	0,
/*	0x05	005	ENQ	Enquiry							*/	0,
/*	0x06	006	ACK	Acknowledge						*/	0,
/*	0x07	007	BEL	Bell								*/	0,
/*	0x08	008	BS		Backspace						*/	0,
/*	0x09	009	HT		Horizontal Tab					*/	0,
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
/*	0x20	032	SPA	Space								*/	0,
/*	0x21	033	!		Exclamation Point				*/	1,
/*	0x22	034	"		Double Quote					*/	0,
/*	0x23	035	#		Pound Sign						*/	1,
/*	0x24	036	$		Dollar Sign						*/	1,
/*	0x25	037	%		Percent							*/	1,
/*	0x26	038	&		Ampersand						*/	1,
/*	0x27	039	'		Apostrophe						*/	1,
/*	0x28	040	(		Right Parenthesis				*/	0,
/*	0x29	041	)		Left Parenthesis				*/	0,
/*	0x2A	042	*		Asterick							*/	1,
/*	0x2B	043	+		Plus								*/	1,
/*	0x2C	044	,		Comma								*/	0,
/*	0x2D	045	-		Minus								*/	1,
/*	0x2E	046	.		Period							*/	0,
/*	0x2F	047	/		Forward Slash					*/	0,
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
/*	0x3A	058	:		Colon								*/	0,
/*	0x3B	059	;		Semi-Colon						*/	0,
/*	0x3C	060	<		Less-than						*/	0,
/*	0x3D	061	=		Equal								*/	0,
/*	0x3E	062	>		Greater-than					*/	0,
/*	0x3F	063	?		Question Mark					*/	0,
/*	0x40	064	@		At									*/	0,
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
/*	0x5B	091	[											*/	0,
/*	0x5C	092	\		Back Slash						*/	1,
/*	0x5D	093	]											*/	1,
/*	0x5E	094	^											*/	0,
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

EXPORT const unsigned char EncodedText[] = {
/*	0x00	000	NUL	Null								*/ 0, 
/*	0x01	001	SOH	Start of Heading				*/ 0,
/*	0x02	002	STX	Start Text						*/ 0,
/*	0x03	003	ETX	End Text							*/	0,
/*	0x04	004	EOT	End of Transmisison			*/	0,
/*	0x05	005	ENQ	Enquiry							*/	0,
/*	0x06	006	ACK	Acknowledge						*/	0,
/*	0x07	007	BEL	Bell								*/	0,
/*	0x08	008	BS		Backspace						*/	0,
/*	0x09	009	HT		Horizontal Tab					*/	0,
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
/*	0x20	032	SPA	Space								*/	0,
/*	0x21	033	!		Exclamation Point				*/	1,
/*	0x22	034	"		Double Quote					*/	1,
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
/*	0x3F	063	?		Question Mark					*/	0,
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
/*	0x5C	092	\		Back Slash						*/	1,
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


static int
RFC1522_B_Decode(StreamStruct *Codec, StreamStruct *NextCodec)
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
					goto BDecodeDone;	/* Must have had CR/LF at the end */
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

BDecodeDone:
	EndFlushStream;

        if (Len >= Codec->Min) {
            return(Len);
        }

        return(Codec->Len);
}


static int
RFC1522_B_Encode(StreamStruct *Codec, StreamStruct *NextCodec)
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
	}

	if ((NextCodec->Len > 0) || (Codec->EOS)) {
	    unsigned long bytesProcessed;

	    if ((Len == Codec->Len) && (Codec->EOS)) {
		Codec->State = 0;
		NextCodec->EOS = TRUE;
	    }

	    bytesProcessed = NextCodec->Codec(NextCodec, NextCodec->Next);
	    if (bytesProcessed) {
		if (bytesProcessed >= NextCodec->Len) {
		    NextCodec->Len = 0;
		} else {
		    NextCodec->Len -= bytesProcessed;
		    memmove(NextCodec->Start, Out-NextCodec->Len, NextCodec->Len);
		}
	    }
	}

        if (Len >= Codec->Min) {
            return(Len);
        }

        return(Codec->Len);
}


static int
RFC1522_Q_Decode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	*In=Codec->Start;
	unsigned char	*Out=NextCodec->Start+NextCodec->Len;
	unsigned char	*OutEnd=NextCodec->End;
	unsigned long	Len=0;


	while (Len<Codec->Len) {
		FlushOutStream(6);

		if (In[0]=='=') {
			if ((Codec->Len-Len)>=3) {
				In++;
				Len++;
				if ((In[0]=='\r') || (In[0]=='\n')) {
					/* According to spec, a '=\r\n' sequence is a soft newline and get's eaten; we also try to handle '=\n' only */
					In++;
					Len++;
					if (In[0]=='\n') {
						In++;
						Len++;
					}
				} else {
					/* It's a hex char to be decoded */
					In[0] |= 0x20;
					if (In[0] > 64) {
						Out[0]=(In[0]-'a'+10)<<4;
					} else {
						Out[0]=(In[0]-'0')<<4;
					}
					In++;
					Len++;

					In[0] |= 0x20;
					if (In[0] > 64) {
						Out[0] |= (In[0]-'a'+10);
					} else {
						Out[0] |= (In[0]-'0');
					}
					Out++;
					NextCodec->Len++;
					In++;
					Len++;
				}
			} else {
				if (Codec->EOS) {
					/* No more data, let's fake it */
					Out[0]=In[0];
					Out++;
					NextCodec->Len++;
					In++;
					Len++;
				} else {
					/* Need more data? */
				    if (Len >= Codec->Min) {
					return(Len);
				    }

				    return(Codec->Len);
				}
			}
		} else {
			if (In[0]=='_') {
				In[0]=' ';
			}
			Out[0]=In[0];
			Out++;
			NextCodec->Len++;
			In++;
			Len++;
		}
	}

	EndFlushStream;

	return(Len);
}

static	unsigned char *HEX_RFC1522 = {"0123456789ABCDEF"};
#define	MAX_CHARS_PER_LINE	75

static int
RFC1522_Q_Encode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	*In=Codec->Start;
	unsigned char	*Out=NextCodec->Start+NextCodec->Len;
	unsigned char	*OutEnd=NextCodec->End;
	unsigned long	Len=0;


	while (Len<Codec->Len) {
		FlushOutStream(6);

		if ((In[0]<0x20) || (In[0]>0x7f)) {
			
			Out[0]='=';
			Out[1]=HEX_RFC1522[In[0] >> 4];
			Out[2]=HEX_RFC1522[In[0] & 0x0f];
			In++;
			Len++;
			Out+=3;
			NextCodec->Len+=3;
		} else if (In[0]==' ') {
			Out[0]='_';
			In++;
			Len++;
			Out++;
			NextCodec->Len++;
		} else if (In[0]=='=') {
			Out[0]='=';
			Out[1]=HEX_RFC1522[In[0] >> 4];
			Out[2]=HEX_RFC1522[In[0] & 0x0f];
			In++;
			Len++;
			Out+=3;
			NextCodec->Len+=3;
		} else {
			Out[0]=In[0];
			In++;
			Len++;
			Out++;
			NextCodec->Len++;
		}
	}

	EndFlushStream;

	return(Len);
}




static int
RFC1522_Decode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	*In=Codec->Start;
	unsigned char	*Out=NextCodec->Start+NextCodec->Len;
	unsigned char	*OutEnd=NextCodec->End;
	unsigned long	Len=0;
	unsigned long	i;
	StreamStruct	*CharsetCodec;
	StreamStruct	*DecodingCodec;

	while (Len<Codec->Len) {
		FlushOutStream(6);

		if (In[0] != '=') {		
NonEncodedWord:				/* This is not an encoded word; pass it through */
			Out[0]=In[0];
			Out++;
			In++;
			NextCodec->Len++;
			Len++;
			continue;
		} else {
			/* Possible encoded word!															*/
			/* Look for								=?Charset?Q?Encoded-Text?=				*/
			/* This code is designed to return to pass through mode as quickly	*/
			/* as possible if the current stream content is not an encoded word	*/

			unsigned char	*Charset;
			unsigned char	*Encoding;
			unsigned char	*String;
			unsigned char	*End;
			unsigned char	*endPtr;
			unsigned char  *ptr = NULL;

			/* check second character for '?' */
			if ((Len + 1) < Codec->Len) {
				if (In[1] != '?') {
					goto NonEncodedWord;
				}

				endPtr = Codec->Start + Codec->Len;

				ptr = In + 2;
				Charset = ptr;

				/* Look for next '?' */
				while (ptr < endPtr) {
					if (Token[*ptr]) {
						ptr++;
						continue;
					}

					if (*ptr == '?') {
						ptr++;
						if (ptr < endPtr) {
							if ((toupper(*ptr) == 'B') || (toupper(*ptr) == 'Q'))	{
								Encoding = ptr;
								ptr++;
								if (ptr < endPtr) {
									if (*ptr == '?') {
										ptr++;
										String = ptr;
										while (ptr < endPtr) {
											if (EncodedText[*ptr]) {
												ptr++;
												continue;
											}			

											if (*ptr == '?') {
												ptr++;
												if (ptr < endPtr) {
													if (*ptr == '=') {
														/* We have an encoded word! */
														End = ptr + 1;
														
														Encoding[-1]='\0';
														String[-1]='\0';
														End[-2]='\0';

														/* Prepare stream lookups */
														DecodingCodec=NULL;
														if (toupper(Encoding[0])=='Q') {
															DecodingCodec=GetStream("RFC1522Q", FALSE);
														} else if (toupper(Encoding[0])=='B') {
															DecodingCodec=GetStream("RFC1522B", FALSE);
														}

														if (DecodingCodec) {
															CharsetCodec = GetStream(Charset, FALSE);
															if (CharsetCodec) {

																/* Now, Fix up In & Len */
																Len = End - In;
																In = End;

																/* Now build the chain, we will encode from/to */
																i = End -2 - String;
																DecodingCodec->Start = String;
																DecodingCodec->End = String + i;
																DecodingCodec->Len = i;
																DecodingCodec->EOS = TRUE;
																DecodingCodec->Next = CharsetCodec;

																CharsetCodec->Next=Codec->Next;
																i = DecodingCodec->Codec(DecodingCodec, DecodingCodec->Next);
																if (!Codec->EOS) {
																	NextCodec->EOS = FALSE;
																}
																In = End;
																Len = In-Codec->Start;
																ReleaseStream(DecodingCodec);
																ReleaseStream(CharsetCodec);
																Encoding[-1] = '?';
																String[-1] = '?';
																End[-2] = '?';
																Out = NextCodec->Start+NextCodec->Len;

																if (Len < Codec->Len) {
																	goto NonEncodedWord;
																}

																return(Len);
															}

															ReleaseStream(DecodingCodec);
															DecodingCodec=NULL;
														}

														Encoding[-1] = '?';
														String[-1] = '?';
														End[-2] = '?';
													}

													goto NonEncodedWord;
												}

												if (((ptr - In) < MAX_ENCODED_WORD_LEN) && !Codec->EOS) {
													/* We need more data to make a decision */
												    if (Len >= Codec->Min) {
													return(Len);
												    }

												    return(Codec->Len);
												}
											}

											goto NonEncodedWord;
										}

										if (((ptr - In) < MAX_ENCODED_WORD_LEN) && !Codec->EOS) {
											/* We need more data to make a decision */
										    if (Len >= Codec->Min) {
											return(Len);
										    }

										    return(Codec->Len);
										}
									}

									goto NonEncodedWord;
								}

								if (((ptr - In) < MAX_ENCODED_WORD_LEN) && !Codec->EOS) {
									/* We need more data to make a decision */
								    if (Len >= Codec->Min) {
									return(Len);
								    }

								    return(Codec->Len);
								}
							}

							goto NonEncodedWord;
						}

						if (((ptr - In) < MAX_ENCODED_WORD_LEN) && !Codec->EOS) {
							/* We need more data to make a decision */
						    if (Len >= Codec->Min) {
							return(Len);
						    }

						    return(Codec->Len);
						}
					}

					goto NonEncodedWord;
				}
			}

			if (((ptr - In) < MAX_ENCODED_WORD_LEN) && !Codec->EOS) {
				/* We need more data to make a decision */
			    if (Len >= Codec->Min) {
				return(Len);
			    }

			    return(Codec->Len);
			}

			goto NonEncodedWord;
		}
	}

	EndFlushStream;

	return(Len);
}


static int
RFC1522_Encode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	*In=Codec->Start;
	unsigned char	*Out=NextCodec->Start+NextCodec->Len;
	unsigned char	*OutEnd=NextCodec->End;
	unsigned long	Len=0;
	unsigned char	*ptr;
	unsigned char	*Start;
	StreamStruct	*CharsetCodec;
	StreamStruct	*EncodingCodec;
	unsigned long	Count;

	while (Len<Codec->Len) {
		FlushOutStream(6);

		if (In[0]<0x80) {
			Out[0]=In[0];
			Out++;
			In++;
			NextCodec->Len++;
			Len++;
		} else {
			/* Have reason to encode; encode from here to the \n or the end of the stream */
			/*
				First, we need to make sure we've got the \n in the buffer, if not, return for more data
				Or, if there's no more data and no \n, handle it anyway...
			 */

			
			for (ptr=In; ptr<Codec->Start+Codec->Len; ptr++) {
				if (*ptr=='\n') {
					/* We found a LF, we've got enough data to decide */
					break;
				}
			}
			
			if (ptr==Codec->Start+Codec->Len) {
				if (!Codec->EOS) {
					/* Gimme more */
				    if (Len >= Codec->Min) {
					return(Len);
				    }

				    return(Codec->Len);
				}
				/* We just use ptr anyway, we are save since from here on out it's only used as a end indicator */
			} else {
				/* Hey, we got here - this means we've got a CR and thus enough data to encode... */
				/* First, determine which encoding to use */
				if (ptr[-1]=='\r') {
					ptr--;
				}
			}

			Start=In;
			Count=0;
			while (Start!=ptr) {
				if (Start[0]>0x7f) {
					Count++;
				}
				Start++;
			}

			/* Get the stream to translate the output into the users charset */
			if ((CharsetCodec=GetStream((unsigned char *)Codec->Charset, TRUE))==NULL) {
				In++;
				Len++;
				continue;
			}

			if (Count>10) {
				/* Lot's of chars, let's use BASE64 */
				EncodingCodec=GetStream("RFC1522B", TRUE);
			} else {
				/* Not many 8-bit chars, let's use QP */
				EncodingCodec=GetStream("RFC1522Q", TRUE);
			}


			/* Flush our output buffer; we've be putting a whole lot of data in it and need to make sure there's space */
			FlushOutStream(30);

			/* Encoding indicator */
			Count=snprintf(Out, OutEnd - Out, "=?%s?%c?", (char *)Codec->Charset, Count>10 ? 'B' : 'Q');
			Out+=Count;
			NextCodec->Len+=Count;

			/* Now build the chain, we will encode from/to */
			Count=ptr-In;
			CharsetCodec->Start=In;
			CharsetCodec->End=In+Count;
			CharsetCodec->EOS=TRUE;
			CharsetCodec->Next=EncodingCodec;
			CharsetCodec->Len=Count;

			EncodingCodec->Next=Codec->Next;

			if ((Count=CharsetCodec->Codec(CharsetCodec, CharsetCodec->Next))==0) {
XplConsolePrintf("Trouble - RFC1522Codec return 0 bytes processed\n");
				Count=ptr-In;
				if (Count==0) {
XplConsolePrintf("Double Trouble - RFC1522Codec ptr-In also 0\n");
					Count=Codec->Len;
				}
			}

			In+=Count;
			Len+=Count;
			ReleaseStream(EncodingCodec);
			ReleaseStream(CharsetCodec);
			/* Now the terminator */
			Out=NextCodec->Start+NextCodec->Len;
			Out[0]='?';
			Out[1]='=';
			NextCodec->Len+=2;
			Out+=2;
		}
	}

	EndFlushStream;

	return(Len);
}

/* this codec can be used for rff2822 'display-name' in the From: To: and CC: header fields */
/* it will produce a quoted string or a rfc2047 encoded word */
/* input streams must have EOS to use this codec */
/* if the entire stream does not fit in the buffer, it will get tossed */
/* it expects input streams to be in utf-8 */

static int
RFC2047_Name_Encode(StreamStruct *Codec, StreamStruct *NextCodec)
{
    unsigned char	*In=Codec->Start;
    unsigned char	*Out=NextCodec->Start+NextCodec->Len;
    unsigned char	*OutEnd=NextCodec->End;
    unsigned long	Len = 0;
    unsigned char	*ptr;
    StreamStruct	*CharsetCodec;
    StreamStruct	*EncodingCodec;
    unsigned long	Count;

    unsigned long	nonQtextChars;
    unsigned char	*InEnd;
    BOOL				needTransportEncoding;

    if (Codec->EOS) {
	/* determine if tranport encoding is needed */
	/* count the characters that don't need encoded */
	ptr = Codec->Start;
	InEnd = Codec->Start + Codec->Len;
	nonQtextChars = 0;
	needTransportEncoding = FALSE;
	while (ptr < InEnd) {
	    if (quotableText[*ptr]) {
		ptr++;
		continue;
	    }

	    if ((*ptr == '"') || (*ptr == '\\'))  {
		nonQtextChars++;
		ptr++;
		continue;
	    }

	    needTransportEncoding = TRUE;
	    nonQtextChars++;
	    ptr++;

	    while (ptr < InEnd) {
		if (quotableText[*ptr]) {
		    ptr++;
		    continue;
		}
		nonQtextChars++;
		ptr++;
		continue;
	    }
	}

	if (!needTransportEncoding) {
	    /* create a quoted string */
	    FlushOutStream(1);
	    *Out = '"';
	    Out++;
	    NextCodec->Len++;

	    while (Len < Codec->Len) {
		if ((*In != '\\') && (*In != '"'))	{
		    FlushOutStream(1);
		    *Out = (unsigned char)*In;
		    Out++;
		    In++;
		    NextCodec->Len++;
		    Len++;
		    continue;
		} else {
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
	    }

	    FlushOutStream(1);
	    *Out = '"';
	    Out++;
	    NextCodec->Len++;

	    EndFlushStream;
	    return(Len);
	}

	while (Len<Codec->Len) {
	    FlushOutStream(6);

	    /* Get the stream to translate the output into the users charset */
	    if ((CharsetCodec=GetStream((unsigned char *)Codec->Charset, TRUE))==NULL) {
		In++;
		Len++;
		continue;
	    }

	    if (nonQtextChars > 10) {
		/* Lot's of chars, let's use BASE64 */
		EncodingCodec = GetStream("RFC1522B", TRUE);
	    } else {
		/* Not many 8-bit chars, let's use QP */
		EncodingCodec = GetStream("RFC1522Q", TRUE);
	    }

	    /* Flush our output buffer; we've be putting a whole lot of data in it and need to make sure there's space */
	    FlushOutStream(30);

	    /* Encoding indicator */
	    Count = snprintf(Out, OutEnd - Out, "=?%s?%c?", (char *)Codec->Charset, nonQtextChars > 10 ? 'B' : 'Q');
	    Out+=Count;
	    NextCodec->Len+=Count;

	    /* Now build the chain, we will encode from/to */
	    Count=ptr-In;
	    CharsetCodec->Start=In;
	    CharsetCodec->End=In+Count;
	    CharsetCodec->EOS=TRUE;
	    CharsetCodec->Next=EncodingCodec;
	    CharsetCodec->Len=Count;

	    EncodingCodec->Next=Codec->Next;

	    if ((Count=CharsetCodec->Codec(CharsetCodec, CharsetCodec->Next))==0) {
		Count=ptr-In;
		if (Count==0) {
		    Count=Codec->Len;
		}
	    }

	    In+=Count;
	    Len+=Count;
	    ReleaseStream(EncodingCodec);
	    ReleaseStream(CharsetCodec);
	    /* Now the terminator */
	    Out=NextCodec->Start+NextCodec->Len;
	    Out[0]='?';
	    Out[1]='=';
	    NextCodec->Len+=2;
	    Out+=2;
	}

	EndFlushStream;
	return(Len);
    }

    if (Len >= Codec->Min) {
	return(Len);
    }

    /* We have to flush the buffer */
    return(Codec->Len);
}

