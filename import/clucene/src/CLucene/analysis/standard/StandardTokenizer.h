/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef _lucene_analysis_standard_StandardTokenizer
#define _lucene_analysis_standard_StandardTokenizer

#if defined(_LUCENE_PRAGMA_ONCE)
# pragma once
#endif

#include "../AnalysisHeader.h"
#include "../Analyzers.h"
#include "StandardTokenizerConstants.h"
#include "CLucene/util/StringBuffer.h"
#include "CLucene/util/FastCharStream.h"
#include "CLucene/util/Reader.h"


CL_NS_DEF2(analysis,standard)

  // This should be a good tokenizer for most European-language documents.
  class StandardTokenizer: public Tokenizer {
  private:
    int32_t rdPos;
    int32_t tokenStart;

    // Advance by one character, incrementing rdPos and returning the character.
    int readChar();
    // Retreat by one character, decrementing rdPos.
    void unReadChar();

    // createToken centralizes token creation for auditing purposes.
	//Token* createToken(CL_NS(util)::StringBuffer* sb, TokenTypes tokenCode);
    inline bool setToken(Token* t, CL_NS(util)::StringBuffer* sb, TokenTypes tokenCode);

    bool ReadDotted(CL_NS(util)::StringBuffer* str, TokenTypes forcedType,Token* t);

  public:
	CL_NS(util)::FastCharStream* rd;

    // Constructs a tokenizer for this Reader.
    StandardTokenizer(CL_NS(util)::Reader* reader);

    ~StandardTokenizer();

	  /** Returns the next token in the stream, or false at end-of-stream.
	  * The returned token's type is set to an element of
	  * StandardTokenizerConstants::tokenImage. */
    bool next(Token* token);

    // Reads for number like "1"/"1234.567", or IP address like "192.168.1.2".
    bool ReadNumber(const TCHAR* previousNumber, const TCHAR prev, Token* t);

    bool ReadAlphaNum(const TCHAR prev, Token* t);

    // Reads for apostrophe-containing word.
    bool ReadApostrophe(CL_NS(util)::StringBuffer* str, Token* t);

    // Reads for something@... it may be a COMPANY name or a EMAIL address
    bool ReadAt(CL_NS(util)::StringBuffer* str, Token* t);

    // Reads for COMPANY name like AT&T.
    bool ReadCompany(CL_NS(util)::StringBuffer* str, Token* t);
  };

CL_NS_END2
#endif
