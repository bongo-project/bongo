/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#include "CLucene/StdHeader.h"
#include "QueryParser.h"

#include "QueryParserConstants.h"
#include "CLucene/analysis/AnalysisHeader.h"
#include "CLucene/util/Reader.h"
#include "CLucene/search/SearchHeader.h"
#include "CLucene/index/Term.h"

#include "CLucene/search/TermQuery.h"
#include "CLucene/search/PhraseQuery.h"
#include "CLucene/search/RangeQuery.h"
#include "CLucene/search/PrefixQuery.h"
#include "CLucene/search/WildcardQuery.h"
#include "CLucene/search/FuzzyQuery.h"
#include "CLucene/search/PrefixQuery.h"

#include "TokenList.h"
#include "QueryToken.h"
#include "QueryParserBase.h"
#include "Lexer.h"

CL_NS_USE(util)
CL_NS_USE(index)
CL_NS_USE(analysis)
CL_NS_USE(search)

CL_NS_DEF(queryParser)

    QueryParser::QueryParser(const TCHAR* _field, Analyzer* _analyzer) : analyzer(_analyzer){
    //Func - Constructor.
	//       Instantiates a QueryParser for the named field _field
	//Pre  - _field != NULL
	//Post - An instance has been created

        CND_PRECONDITION(_field != NULL, "_field is NULL");
//todo: xxx can we have fixed length fields? must change throughout library
		field = stringDuplicate( _field);
		tokens = NULL;
		lowercaseWildcardTerms = true;
	}

	QueryParser::~QueryParser() {
	//Func - Destructor
	//Pre  - true
	//Post - The instance has been destroyed

        _CLDELETE_CARRAY(field);
		//_CLDELETE(tokens); bvk: moved to the parser function
      //                 memory leak would occur if we setup a queryParser object
      //                 and called Parse several times
	}

    //static
    Query* QueryParser::parse(const TCHAR* query, const TCHAR* field, Analyzer* analyzer){
    //Func - Returns a new instance of the Query class with a specified query, field and
    //       analyzer values.
    //Pre  - query != NULL and holds the query to parse
	//       field != NULL and holds the default field for query terms
	//       analyzer holds a valid reference to an Analyzer and is used to
	//       find terms in the query text
	//Post - query has been parsed and an instance of Query has been returned

		CND_PRECONDITION(query != NULL, "query is NULL");
        CND_PRECONDITION(field != NULL, "field is NULL");

		QueryParser parser(field, analyzer);
		return parser.parse(query);
	}

    Query* QueryParser::parse(const TCHAR* query){
	//Func - Returns a parsed Query instance
	//Pre  - query != NULL and contains the query value to be parsed
	//Post - Returns a parsed Query Instance

        CND_PRECONDITION(query != NULL, "query is NULL");

		//Instantie a Stringer that can read the query string
        Reader* r = _CLNEW StringReader(query);

		//Check to see if r has been created properly
		CND_CONDITION(r != NULL, "Could not allocate memory for StringReader r");

		//Pointer for the return value
		Query* ret = NULL;

		try{
			//Parse the query managed by the StringReader R and return a parsed Query instance
			//into ret
			ret = parse(r);
		}_CLFINALLY (
			_CLDELETE(r);
		);

		//Check to if the ret points to a valid instance of Query
        CND_CONDITION(ret != NULL, "ret is NULL");

		return ret;
	}

	Query* QueryParser::parse(Reader* reader){
	//Func - Returns a parsed Query instance
	//Pre  - reader contains a valid reference to a Reader and manages the query string
	//Post - A parsed Query instance has been returned or

		//instantiate the TokenList tokens
		TokenList _tokens;
		this->tokens = &_tokens;

		//Instantiate a lexer
		Lexer lexer(reader);

		//tokens = lexer.Lex();
		//Lex the tokens
		lexer.Lex(tokens);

		//Peek to the first token and check if is an EOF
		if (tokens->peek()->Type == CL_NS(queryParser)::EOF_){
			// The query string failed to yield any tokens.  We discard the
			// TokenList tokens and raise an exceptioin.
			this->tokens = NULL;
		   _CLTHROWA(CL_ERR_Parse, "No query given.");
		}

		//Return the parsed Query instance
		Query* ret = MatchQuery(field);
		this->tokens = NULL;
      return ret;
	}

	int32_t QueryParser::MatchConjunction(){
	//Func - matches for CONJUNCTION
	//       CONJUNCTION ::= <AND> | <OR>
	//Pre  - tokens != NULL
	//Post - if the first token is an AND or an OR then
	//       the token is extracted and deleted and CONJ_AND or CONJ_OR is returned
	//       otherwise CONJ_NONE is returned

        CND_PRECONDITION(tokens != NULL, "tokens is NULL");

		switch(tokens->peek()->Type){
			case CL_NS(queryParser)::AND_ :{
				//Delete the first token of tokenlist
				ExtractAndDeleteToken();
				return CONJ_AND;
				}
			case CL_NS(queryParser)::OR   :{
				//Delete the first token of tokenlist
				ExtractAndDeleteToken();
				return CONJ_OR;
				}
			default : {
				return CONJ_NONE;
				}
			}
	}

	int32_t QueryParser::MatchModifier(){
	//Func - matches for MODIFIER
	//       MODIFIER ::= <PLUS> | <MINUS> | <NOT>
	//Pre  - tokens != NULL
	//Post - if the first token is a PLUS the token is extracted and deleted and MOD_REQ is returned
	//       if the first token is a MINUS or NOT the token is extracted and deleted and MOD_NOT is returned
	//       otherwise MOD_NONE is returned
		CND_PRECONDITION(tokens != NULL, "tokens is NULL");

		switch(tokens->peek()->Type){
			case CL_NS(queryParser)::PLUS :{
				//Delete the first token of tokenlist
				ExtractAndDeleteToken();
				return MOD_REQ;
				}

			case CL_NS(queryParser)::MINUS :
			case CL_NS(queryParser)::NOT   :{
				//Delete the first token of tokenlist
				ExtractAndDeleteToken();
				return MOD_NOT;
				}
			default :{
				return MOD_NONE;
				}
			}
	}

	Query* QueryParser::MatchQuery(const TCHAR* field){
	//Func - matches for QUERY
	//       QUERY ::= [MODIFIER] QueryParser::CLAUSE (<CONJUNCTION> [MODIFIER] CLAUSE)*
	//Pre  - field != NULL
	//Post -

		CND_PRECONDITION(tokens != NULL, "tokens is NULL");

		CLVector<BooleanClause*> clauses(false);

		Query* q = NULL;
		//Query* firstQuery = NULL; //bvk: why do this?
		//bug: bvk: if the firstQuery turns out to be null
		//for example if the first clause is an empty phrase
		//then booleanquery ends up being created...

		int32_t mods = MOD_NONE;
		int32_t conj = CONJ_NONE;

		//match for MODIFIER
		mods = MatchModifier();

		//match for CLAUSE
		q = MatchClause(field);
		AddClause(&clauses, CONJ_NONE, mods, q);

		//if(mods == MOD_NONE){
		//	firstQuery = q;
		//}

		// match for CLAUSE*
		while(true){
			QueryToken* p = tokens->peek();
			if(p->Type == CL_NS(queryParser)::EOF_){
				QueryToken* qt = MatchQueryToken(CL_NS(queryParser)::EOF_);
				_CLDELETE(qt);
				break;
			}

			if(p->Type == CL_NS(queryParser)::RPAREN){
				//MatchQueryToken(CL_NS(queryParser)::RPAREN);
				break;
			}

			//match for a conjuction (AND OR NOT)
			conj = MatchConjunction();
			//match for a modifier
			mods = MatchModifier();

			q = MatchClause(field);
			if ( q != NULL )
				AddClause(&clauses, conj, mods, q);
		}

		// finalize query
		if(clauses.size() == 1){ //bvk: removed this && firstQuery != NULL
			BooleanClause* c = clauses[0];
			Query* q = c->query;

			//Condition check to be sure clauses[0] is valid
			CND_CONDITION(c != NULL, "c is NULL");

			//Tell the boolean clause not to delete its query
			c->deleteQuery=false;
			//Clear the clauses list
			clauses.clear();
			_CLDELETE(c);

			return q;
		}else{
			BooleanQuery* query = _CLNEW BooleanQuery();
			//Condition check to see if query has been allocated properly
			CND_CONDITION(query != NULL, "No memory could be allocated for query");

			//iterate through all the clauses
			for( uint32_t i=0;i<clauses.size();i++ ){
				//Condition check to see if clauses[i] is valdid
				CND_CONDITION(clauses[i] != NULL, "clauses[i] is NULL");
				//Add it to query
				query->add(clauses[i]);
			}
			return query;
		}
	}

	Query* QueryParser::MatchClause(const TCHAR* field){
	//Func - matches for CLAUSE
	//       CLAUSE ::= [TERM <COLONQueryParser::>] ( TERM | (<LPAREN> QUERY <RPAREN>))
	//Pre  - field != NULL
	//Post -

		CND_PRECONDITION(field != NULL, "field is NULL");

		Query* q = NULL;
		const TCHAR* sfield = field;
		bool delField = false;

		QueryToken *DelToken = NULL;

		//match for [TERM <COLON>]
		QueryToken* term = tokens->extract();
		if(term->Type == CL_NS(queryParser)::TERM && tokens->peek()->Type == CL_NS(queryParser)::COLON){
			DelToken = MatchQueryToken(CL_NS(queryParser)::COLON);

			CND_CONDITION(DelToken != NULL,"DelToken is NULL");
			_CLDELETE(DelToken);

			sfield = discardEscapeChar(term->Value);
			delField= true;
			_CLDELETE(term);
		}else{
			tokens->push(term);
		}

		// match for
		// TERM | (<LPAREN> QUERY <RPAREN>)
		if(tokens->peek()->Type == CL_NS(queryParser)::LPAREN){
			DelToken = MatchQueryToken(CL_NS(queryParser)::LPAREN);

			CND_CONDITION(DelToken != NULL,"DelToken is NULL");
			_CLDELETE(DelToken);

			q = MatchQuery(sfield);
			//DSR:2004.11.01:
			//If exception is thrown while trying to match trailing parenthesis,
			//need to prevent q from leaking.

			try{
			   DelToken = MatchQueryToken(CL_NS(queryParser)::RPAREN);

			   CND_CONDITION(DelToken != NULL,"DelToken is NULL");
			   _CLDELETE(DelToken);

			}catch(...) {
				_CLDELETE(q);
				throw;
			}
		}else{
			q = MatchTerm(sfield);
		}

		if (delField){
			_CLDELETE_CARRAY(sfield);
		}

	  return q;
	}


	Query* QueryParser::MatchTerm(const TCHAR* field){
	//Func - matches for TERM
	//       TERM ::= TERM | PREFIXTERM | WILDTERM | NUMBER
	//                [ <FUZZY> ] [ <CARAT> <NUMBER> [<FUZZY>]]
	//			      | (<RANGEIN> | <RANGEEX>) [<CARAT> <NUMBER>]
	//			      | <QUOTED> [SLOP] [<CARAT> <NUMBER>]
	//Pre  - field != NULL
	//Post -
		CND_PRECONDITION(field != NULL,"field is NULL");

		QueryToken* term = NULL;
		QueryToken* slop = NULL;
		QueryToken* boost = NULL;

		bool prefix = false;
		bool wildcard = false;
		bool fuzzy = false;
		bool rangein = false;
		Query* q = NULL;

		term = tokens->extract();
		QueryToken* DelToken = NULL; //Token that is about to be deleted

		switch(term->Type){
			case CL_NS(queryParser)::TERM:

			case CL_NS(queryParser)::NUMBER:

#ifndef NO_PREFIX_QUERY
			case CL_NS(queryParser)::PREFIXTERM:
#endif
#ifndef NO_WILDCARD_QUERY
			case CL_NS(queryParser)::WILDTERM:
#endif
			{ //start case
#ifndef NO_PREFIX_QUERY
				//Check if type of QueryToken term is a prefix term
				if(term->Type == CL_NS(queryParser)::PREFIXTERM){
					prefix = true;
				}
#endif
#ifndef NO_WILDCARD_QUERY
				//Check if type of QueryToken term is a wildcard term
				if(term->Type == CL_NS(queryParser)::WILDTERM){
					wildcard = true;
				}
#endif

#ifndef NO_FUZZY_QUERY
				//Peek to see if the type of the next token is fuzzy term
				if(tokens->peek()->Type == CL_NS(queryParser)::FUZZY){
					DelToken = MatchQueryToken(CL_NS(queryParser)::FUZZY);

					CND_CONDITION(DelToken !=NULL, "DelToken is NULL");
					_CLDELETE(DelToken);

					fuzzy = true;
				}
#endif
				if(tokens->peek()->Type == CL_NS(queryParser)::CARAT){
					DelToken = MatchQueryToken(CL_NS(queryParser)::CARAT);

					CND_CONDITION(DelToken !=NULL, "DelToken is NULL");
					_CLDELETE(DelToken);

					boost = MatchQueryToken(CL_NS(queryParser)::NUMBER);
#ifndef NO_FUZZY_QUERY
				   if(tokens->peek()->Type == CL_NS(queryParser)::FUZZY){
					   DelToken = MatchQueryToken(CL_NS(queryParser)::FUZZY);

					   CND_CONDITION(DelToken !=NULL, "DelToken is NULL");
					   _CLDELETE(DelToken);

					   fuzzy = true;
				   }
#endif
				} //end if type==CARAT

				if (false){
				}
#ifndef NO_WILDCARD_QUERY
				else if(wildcard){
					Term* t = NULL;
					TCHAR* tmp = discardEscapeChar(term->Value);
					if ( lowercaseWildcardTerms ){
						_tcslwr(tmp);
						t = _CLNEW Term(field, tmp);
					}else{
						t = _CLNEW Term(field, tmp);
					}
					q = _CLNEW WildcardQuery(t);
					_CLDECDELETE(t);
					_CLDELETE_CARRAY(tmp);
					break;
				}
#endif
#ifndef NO_PREFIX_QUERY
				else if(prefix){
					//Create a PrefixQuery
					q = GetPrefixQuery(field,term->Value,lowercaseExpandedTerms);
					break;
				}
#endif
#ifndef NO_FUZZY_QUERY
				else if(fuzzy){
					//Create a FuzzyQuery
					q = GetFuzzyQuery(field,term->Value,lowercaseExpandedTerms);
					break;
				}
#endif
				else{
					q = GetFieldQuery(field, analyzer, term->Value);
					break;
				}
			}//case CL_NS(queryParser)::TERM:
			 //case CL_NS(queryParser)::NUMBER:
			 //case CL_NS(queryParser)::PREFIXTERM:
			 //case CL_NS(queryParser)::WILDTERM:

#ifndef NO_RANGE_QUERY
			case CL_NS(queryParser)::RANGEIN:
			case CL_NS(queryParser)::RANGEEX:{

				if(term->Type == CL_NS(queryParser)::RANGEIN){
					rangein = true;
				}

				if(tokens->peek()->Type == CL_NS(queryParser)::CARAT){
					DelToken = MatchQueryToken(CL_NS(queryParser)::CARAT);

					CND_CONDITION(DelToken !=NULL, "DelToken is NULL");
					_CLDELETE(DelToken);

					boost = MatchQueryToken(CL_NS(queryParser)::NUMBER);
				}

				TCHAR* tmp = stringDuplicate ( term->Value +1);
				tmp[_tcslen(tmp)-1] = 0;
				q = GetRangeQuery(field, analyzer,	tmp, rangein);
				_CLDELETE_ARRAY(tmp);
				break;
			} //case CL_NS(queryParser)::RANGEIN:
			//case CL_NS(queryParser)::RANGEEX:
#endif
			case CL_NS(queryParser)::QUOTED:{
				if(tokens->peek()->Type == CL_NS(queryParser)::SLOP){
					slop = MatchQueryToken(CL_NS(queryParser)::SLOP);
				}

				if(tokens->peek()->Type == CL_NS(queryParser)::CARAT){
					DelToken = MatchQueryToken(CL_NS(queryParser)::CARAT);

					CND_CONDITION(DelToken !=NULL, "DelToken is NULL");
					_CLDELETE(DelToken);

					boost = MatchQueryToken(CL_NS(queryParser)::NUMBER);
				}

				//todo: check term->Value+1
				TCHAR* quotedValue = STRDUP_TtoT(term->Value+1);
				quotedValue[_tcslen(quotedValue)-1] = '\0';
				q = GetFieldQuery(field, analyzer, quotedValue);
				_CLDELETE_CARRAY(quotedValue);
				if(slop != NULL && q != NULL && q->instanceOf(PhraseQuery::getClassName()) ){
				   try {
                       TCHAR* end;
					   int32_t s = (int32_t)_tcstoi64(slop->Value+1, &end, 10);
					   ((PhraseQuery*)q)->setSlop( s );
				   }catch(...){ //todo: only catch IO Err???
					   //ignored
				   }
				}
   				_CLDELETE(slop);//bvk: moved delting of slop to after
   				              //the PhraseQuery check. Otherwise use of slop
   				              //with non-phrasequeries will leak memory
			} //case CL_NS(queryParser)::QUOTED:
		} // end of switch

		_CLDELETE(term);


		if( q!=NULL && boost != NULL ){
			float_t f = 1.0F;
			try {
				//TODO: check this
				TCHAR* tmp;
				f = _tcstod(boost->Value, &tmp);
				//f = Single.Parse(boost->Value, NumberFormatInfo.InvariantInfo);
			}catch(...){ //todo: only catch IO Err???
				//ignored
			}
			_CLDELETE(boost);

			q->setBoost( f);
		}

		return q;
	}

	QueryToken* QueryParser::MatchQueryToken(QueryTokenTypes expectedType){
	//Func - matches for QueryToken of the specified type and returns it
	//       otherwise Exception throws
	//Pre  - tokens != NULL
	//Post -

		CND_PRECONDITION(tokens != NULL,"tokens is NULL");

		if(tokens->count() == 0){
			QueryParserBase::throwParserException(_T("Error: Unexpected end of program"),' ',0,0);
		}

	  //Extract a token form the TokenList tokens
	  QueryToken* t = tokens->extract();
	  //Check if the type of the token t matches the expectedType
	  if (expectedType != t->Type){
		  TCHAR buf[200];
		  _sntprintf(buf,200,_T("Error: Unexpected QueryToken: %d, expected: %d"),t->Type,expectedType);
		  _CLDELETE(t);
		  QueryParserBase::throwParserException(buf,' ',0,0);
		  }

	  //Return the matched token
	  return t;
	}

	void QueryParser::ExtractAndDeleteToken(void){
	//Func - Extracts the first token from the Tokenlist tokenlist
	//       and destroys it
	//Pre  - true
	//Post - The first token has been extracted and destroyed

		CND_PRECONDITION(tokens != NULL, "tokens is NULL");

		//Extract the token from the TokenList tokens
		QueryToken* t = tokens->extract();
		//Condition Check Token may not be NULL
		CND_CONDITION(t != NULL, "Token is NULL");
		//Delete Token
		_CLDELETE(t);
	}

CL_NS_END
