/* query parser bits
 */

#ifndef QUERY_PARSER_H
#define QUERY_PARSER_H

#define	DEBUG_MESSAGE(...)	

struct expression {
	char *op;
	
	void *exp1;
	void *exp2;
	
	int exp1_const;
	int exp2_const;
	
	int exp1_type;
	int exp2_type;
};

struct parser_state {
	struct expression *start;
	struct expression *last;
	int entries;
	char *query;
	char *query_ptr;
};

int	QueryParserStart(struct parser_state *state, const char *query, int max_expr);
int	QueryParserRun(struct parser_state *state, BOOL strict_quotes);
void	QueryParserFinish(struct parser_state *state);

int	QueryParser_IsOp (const char *token);
int	QueryParser_IsProperty (const char *token);
int QueryParser_IsNumber (const char *token);
int QueryParser_IsString (const char *token);

void	output_expression(struct expression *e);

#endif
