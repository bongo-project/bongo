/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#include "CLucene/StdHeader.h"
#include "Lexer.h"

#include "QueryParserConstants.h"
#include "CLucene/util/FastCharStream.h"
#include "CLucene/util/Reader.h"
#include "CLucene/util/StringBuffer.h"
#include "TokenList.h"
#include "QueryToken.h"
#include "QueryParserBase.h"

CL_NS_USE(util)

CL_NS_DEF(queryParser)
Lexer::Lexer(const TCHAR* query) {
   //Func - Constructor
   //Pre  - query != NULL and contains the query string
   //Post - An instance of Lexer has been created

   CND_PRECONDITION(query != NULL, "query is NULL");

   //The InputStream of Reader must be destroyed in the destructor
   delSR = true;

   StringReader *r = _CLNEW StringReader(query);

   //Check to see if r has been created properly
   CND_CONDITION(r != NULL, "Could not allocate memory for StringReader r");

   //Instantie a FastCharStream instance using r and assign it to reader
   reader = _CLNEW FastCharStream(r);

   //Check to see if reader has been created properly
   CND_CONDITION(reader != NULL, "Could not allocate memory for FastCharStream reader");

   //The InputStream of Reader must be destroyed in the destructor
   delSR = true;

}


Lexer::Lexer(Reader* source) {
   //Func - Constructor
   //       Initializes a new instance of the Lexer class with the specified
   //       TextReader to lex.
   //Pre  - Source contains a valid reference to a Reader
   //Post - An instance of Lexer has been created using source as the reader

   //Instantie a FastCharStream instance using r and assign it to reader
   reader = _CLNEW FastCharStream(source);

   //Check to see if reader has been created properly
   CND_CONDITION(reader != NULL, "Could not allocate memory for FastCharStream reader");

   //The InputStream of Reader must not be destroyed in the destructor
   delSR  = false;
}


Lexer::~Lexer() {
   //Func - Destructor
   //Pre  - true
   //Post - if delSR was true the InputStream input of reader has been deleted
   //       The instance of Lexer has been destroyed

   if (delSR) {
      _CLDELETE(reader->input);
   }

   _CLDELETE(reader);
}


void Lexer::Lex(TokenList *tokenList) {
   //Func - Breaks the input stream onto the tokens list tokens
   //Pre  - tokens != NULL and contains a TokenList in which the tokens can be stored
   //Post - The tokens have been added to the TokenList tokens

   CND_PRECONDITION(tokenList != NULL, "tokens is NULL");

   //Get the next token
   QueryToken* token = NULL;

   //Get all the tokens
   while((token = GetNextToken()) != NULL) {
      //Add the token to the tokens list
      tokenList->add(token);
   }

   //The end has been reached so create an EOF_ token
   token = _CLNEW QueryToken( CL_NS(queryParser)::EOF_);

   //Check to see if token has been created properly
   CND_CONDITION(token != NULL, "Could not allocate memory for QueryToken token");

   //Add the final token to the TokenList _tokens
   tokenList->add(token);
}


QueryToken* Lexer::GetNextToken() {
   while(!reader->Eos()) {
      int ch = reader->GetNext();

	  if ( ch == -1 )
		break;

      // skipping whitespaces
      if( _istspace(ch)!=0 ) {
         continue;
      }
      TCHAR buf[2] = {ch,'\0'};
      switch(ch) {
         case '+':
            return _CLNEW QueryToken(buf , CL_NS(queryParser)::PLUS);
         case '-':
            return _CLNEW QueryToken(buf, CL_NS(queryParser)::MINUS);
         case '(':
            return _CLNEW QueryToken(buf, CL_NS(queryParser)::LPAREN);
         case ')':
            return _CLNEW QueryToken(buf, CL_NS(queryParser)::RPAREN);
         case ':':
            return _CLNEW QueryToken(buf, CL_NS(queryParser)::COLON);
         case '!':
            return _CLNEW QueryToken(buf, CL_NS(queryParser)::NOT);
         case '^':
            return _CLNEW QueryToken(buf, CL_NS(queryParser)::CARAT);
         case '~':
         #ifndef NO_FUZZY_QUERY
            if( _istdigit( reader->Peek() )!=0 ) {
               const TCHAR* number = ReadIntegerNumber(ch);
               QueryToken* ret = _CLNEW QueryToken(number, CL_NS(queryParser)::SLOP);
               _CLDELETE_CARRAY(number);
               return ret;
            }else
            {
               return _CLNEW QueryToken(buf, CL_NS(queryParser)::FUZZY);
            }
         #endif
         case '"':
            return ReadQuoted(ch);
         #ifndef NO_RANGE_QUERY
         case '[':
            return ReadInclusiveRange(ch);
         case '{':
            return ReadExclusiveRange(ch);
         #endif
         case ']':
         case '}':
         case '*':
            QueryParserBase::throwParserException( _T("Unrecognized TCHAR %d at %d::%d."), 
               ch, reader->Column(), reader->Line() );
         default:
            return ReadTerm(ch);

   // end of swith
      }

   }
   return NULL;
}


const TCHAR* Lexer::ReadIntegerNumber(const TCHAR ch) {
   StringBuffer number;
//TODO: check this
   number.appendChar(ch);

   int c = reader->Peek();
   while( c!=-1 && _istdigit(c)!=0 ) {
      number.appendChar(reader->GetNext());
      c = reader->Peek();
   }
   return number.toString();
}


#ifndef NO_RANGE_QUERY
QueryToken* Lexer::ReadInclusiveRange(const TCHAR prev) {
   int ch = prev;
   StringBuffer range;
   range.appendChar(ch);

   while(!reader->Eos()) {
      ch = reader->GetNext();
	  if ( ch == -1 )
		break;
      range.appendChar(ch);

      if(ch == ']')
         return _CLNEW QueryToken(range.getBuffer(), CL_NS(queryParser)::RANGEIN);
   }
   /* DSR:CL_BUG: The old format string contained %s where it should have
    ** contained %d, which caused sprintf (Linux, glibc 2.3.2) to crash
    ** because the char was eventually passed to strlen. */
   QueryParserBase::throwParserException(_T("Unterminated inclusive range! %d %d::%d"),' ',
      reader->Column(),reader->Column());
   return NULL;
}


QueryToken* Lexer::ReadExclusiveRange(const TCHAR prev) {
   int ch = prev;
   StringBuffer range;
   range.appendChar(ch);

   while(!reader->Eos()) {
      ch = reader->GetNext();

	  if (ch==-1)
		break;
	  range.appendChar(ch);

      if(ch == '}')
         return _CLNEW QueryToken(range.getBuffer(), CL_NS(queryParser)::RANGEEX);
   }
   /* DSR:CL_BUG: The old format string contained %s where it should have
    ** contained %d, which caused sprintf (Linux, glibc 2.3.2) to crash
    ** because the char was eventually passed to strlen. */
   QueryParserBase::throwParserException(_T("Unterminated exclusive range! %d %d::%d"),' ',
      reader->Column(),reader->Column() );
   return NULL;
}
#endif

QueryToken* Lexer::ReadQuoted(const TCHAR prev) {
   int ch = prev;
   StringBuffer quoted;
   quoted.appendChar(ch);

   while(!reader->Eos()) {
      ch = reader->GetNext();

	  if (ch==-1)
		break;

      quoted.appendChar(ch);

      if(ch == '"')
         return _CLNEW QueryToken(quoted.getBuffer(), CL_NS(queryParser)::QUOTED);
   }
   /* DSR:CL_BUG: The old format string contained %s where it should have
    ** contained %d, which caused sprintf (Linux, glibc 2.3.2) to crash
    ** because the char was eventually passed to strlen. */
   QueryParserBase::throwParserException(_T("Unterminated string! %d %d::%d"),' ',
      reader->Column(),reader->Column());
   return NULL;
}


QueryToken* Lexer::ReadTerm(const TCHAR prev) {
   int ch = prev;
   bool completed = false;
   int32_t asteriskCount = 0;
   bool hasQuestion = false;

   StringBuffer val;

   while(true) {
      switch(ch) {
		  case -1:
			  break;
         case '\\':
         {
            const TCHAR* re = ReadEscape(ch);
            val.append( re );
            _CLDELETE_CARRAY( re );
         }
         break;

         case LUCENE_WILDCARDTERMENUM_WILDCARD_STRING:
            asteriskCount++;
            val.appendChar(ch);
            break;
#ifndef NO_WILDCARD_QUERY
         case LUCENE_WILDCARDTERMENUM_WILDCARD_CHAR:
            hasQuestion = true;
            val.appendChar(ch);
            break;
#endif
         case '\n':
         case '\t':
         case ' ':
         case '+':
         case '-':
         case '!':
         case '(':
         case ')':
         case ':':
         case '^':
#ifndef NO_RANGE_QUERY
         case '[':
         case ']':
         case '{':
         case '}':
#endif
#ifndef NO_FUZZY_QUERY
         case '~':
#endif
         case '"':
            // create _CLNEW QueryToken
            reader->UnGet();
            completed = true;
            break;
         default:
            val.appendChar(ch);
            break;
   // end of switch
      }

      if(completed || ch==-1 || reader->Eos() )
         break;
      else
         ch = reader->GetNext();
   }

   // create new QueryToken
   if(false)
      return NULL;
   #ifndef NO_WILDCARD_QUERY
   else if(hasQuestion)
      return _CLNEW QueryToken(val.getBuffer(), CL_NS(queryParser)::WILDTERM);
   #endif
   #ifndef NO_PREFIX_QUERY
   else if(asteriskCount == 1 && val.getBuffer()[val.length() - 1] == '*')
      return _CLNEW QueryToken(val.getBuffer(), CL_NS(queryParser)::PREFIXTERM);
   #endif
   #ifndef NO_WILDCARD_QUERY
   else if(asteriskCount > 0)
      return _CLNEW QueryToken(val.getBuffer(), CL_NS(queryParser)::WILDTERM);
   #endif
   else if( _tcsicmp(val.getBuffer(), _T("AND"))==0 || _tcscmp(val.getBuffer(), _T("&&"))==0 )
      return _CLNEW QueryToken(val.getBuffer(), CL_NS(queryParser)::AND_);

   else if( _tcsicmp(val.getBuffer(), _T("OR"))==0 || _tcscmp(val.getBuffer(), _T("||"))==0)
      return _CLNEW QueryToken(val.getBuffer(), CL_NS(queryParser)::OR);

   else if( _tcsicmp(val.getBuffer(), _T("NOT"))==0 )
      return _CLNEW QueryToken(val.getBuffer(), CL_NS(queryParser)::NOT);

   else {

      /* bvk: use another way of identifying a number. This method
              is probably not reliable, and uses _tcsupr, which we
              dont have
      TCHAR* lwr = _tcslwr( val.toString() );
      TCHAR* upr = _tcsupr( val.toString() );
      bool n = (_tcscmp(lwr,upr) == 0);
      _CLDELETE_CARRAY(lwr);
      _CLDELETE_CARRAY(upr);
      */
      bool isnum = true;
      int32_t nlen=val.length();
      for (int32_t i=0;i<nlen;i++) {
         TCHAR ch=val.getBuffer()[i];
//todo: should we also check for spaces,probably not???
         if ( _istalpha(ch) ) {
            isnum=false;
            break;
         }
      }

      if ( isnum )
         return _CLNEW QueryToken(val.getBuffer(), CL_NS(queryParser)::NUMBER);
      else
         return _CLNEW QueryToken(val.getBuffer(), CL_NS(queryParser)::TERM);
   }
}


const TCHAR* Lexer::ReadEscape(const TCHAR prev) {
   TCHAR ch = prev;
   StringBuffer val;
   val.appendChar(ch);

   ch = reader->GetNext();
   int32_t idx = _tcscspn( val.getBuffer(), _T("\\+-!():^[]{}\"~*") );
   if(idx == 0) {
      val.appendChar( ch );
      return val.toString();
   }
   /* DSR:CL_BUG: The old format string contained %s where it should have
    ** contained %d, which caused sprintf (Linux, glibc 2.3.2) to crash
    ** because the char was eventually passed to strlen. */
   QueryParserBase::throwParserException(_T("Unrecognized escape sequence at %d %d::%d"), ' ',
      reader->Column(),reader->Line());
   return 0;
}


CL_NS_END
