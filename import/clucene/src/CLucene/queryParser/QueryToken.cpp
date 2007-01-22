/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#include "CLucene/StdHeader.h"
#include "QueryToken.h"

#include "QueryParserConstants.h"

CL_NS_DEF(queryParser)
    QueryToken::QueryToken(const TCHAR* value, const int32_t start, const int32_t end, const QueryTokenTypes type):    	
      #ifndef LUCENE_TOKEN_WORD_LENGTH
      Value( stringDuplicate(value) ),
      #endif
    	Start(start),
    	End(end),
    	Type(type)
    {
       #ifdef LUCENE_TOKEN_WORD_LENGTH
         _tcsncpy(Value,value,LUCENE_TOKEN_WORD_LENGTH);
        Value[LUCENE_TOKEN_WORD_LENGTH];
       #endif
    }
    
    QueryToken::~QueryToken(){
    //Func - Destructor
	//Pre  - true
	//Post - Instance has been destroyed

       #ifndef LUCENE_TOKEN_WORD_LENGTH
    	_CLDELETE_CARRAY( Value );
      #endif
    }
    
    // Initializes a new instance of the Token class LUCENE_EXPORT.
    //
    QueryToken::QueryToken(const TCHAR* value, const QueryTokenTypes type):
    	#ifndef LUCENE_TOKEN_WORD_LENGTH
      Value( stringDuplicate(value) ),
      #endif
    	Start(0),
    	End(0),
    	Type(type)
    {
    	#ifdef LUCENE_TOKEN_WORD_LENGTH
         _tcsncpy(Value,value,LUCENE_TOKEN_WORD_LENGTH);
         Value[LUCENE_TOKEN_WORD_LENGTH];
		 End=_tcslen(Value);
      #endif
    }
    
    // Initializes a new instance of the Token class LUCENE_EXPORT.
    //
    QueryToken::QueryToken(const QueryTokenTypes type):
    	#ifndef LUCENE_TOKEN_WORD_LENGTH
      Value( NULL ),
      #endif
    	Start(0),
    	End(0),
    	Type(type)
    {
      #ifdef LUCENE_TOKEN_WORD_LENGTH
    	Value[0]=NULL;
      #endif
    }
    
    // Returns a string representation of the Token.
    //
    //QueryToken::toString()
    //{
    //	return "<" + Type + "> " + Value;	
    //}
CL_NS_END
