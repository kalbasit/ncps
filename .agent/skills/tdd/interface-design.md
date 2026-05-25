# Interface Design for Testability

Good interfaces make testing natural:

1. **Accept dependencies, don't create them**

   ```typescript
   // Testable
   function processOrder(order, paymentGateway) {}

   // Hard to test
   function processOrder(order) {
     const gateway = new StripeGateway();
   }
   ```

2. **Return results, don't produce side effects**

   ```typescript
   // Testable
   function calculateDiscount(cart): Discount {}

   // Hard to test
   function applyDiscount(cart): void {
     cart.total -= discount;
   }
   ```

3. **Small surface area**
   - Fewer methods = fewer tests needed
   - Fewer params = simpler test setup

## Go Examples

**Accept interfaces, return structs (Go idiom):**

```go
// Testable: depends on interface, caller can swap in a stub
type OrderStore interface {
    Save(ctx context.Context, o *Order) error
    Find(ctx context.Context, id string) (*Order, error)
}

func NewOrderService(store OrderStore) *OrderService { ... }

// Hard to test: concrete dependency created internally
func NewOrderService() *OrderService {
    return &OrderService{store: postgres.New(os.Getenv("DB_URL"))}
}
```

**Return results, don't mutate inputs:**

```go
// Testable: pure function, easy to assert on return value
func ApplyDiscount(cart Cart, pct float64) Cart {
    cart.Total = cart.Total * (1 - pct/100)
    return cart
}

// Hard to test: mutates in place, test must inspect state
func ApplyDiscount(cart *Cart, pct float64) {
    cart.Total = cart.Total * (1 - pct/100)
}
```
