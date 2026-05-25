# Good and Bad Tests

## Good Tests

**Integration-style**: Test through real interfaces, not mocks of internal parts.

```typescript
// GOOD: Tests observable behavior
test("user can checkout with valid cart", async () => {
  const cart = createCart();
  cart.add(product);
  const result = await checkout(cart, paymentMethod);
  expect(result.status).toBe("confirmed");
});
```

Characteristics:

- Tests behavior users/callers care about
- Uses public API only
- Survives internal refactors
- Describes WHAT, not HOW
- One logical assertion per test

## Bad Tests

**Implementation-detail tests**: Coupled to internal structure.

```typescript
// BAD: Tests implementation details
test("checkout calls paymentService.process", async () => {
  const mockPayment = jest.mock(paymentService);
  await checkout(cart, payment);
  expect(mockPayment.process).toHaveBeenCalledWith(cart.total);
});
```

Red flags:

- Mocking internal collaborators
- Testing private methods
- Asserting on call counts/order
- Test breaks when refactoring without behavior change
- Test name describes HOW not WHAT
- Verifying through external means instead of interface

```typescript
// BAD: Bypasses interface to verify
test("createUser saves to database", async () => {
  await createUser({ name: "Alice" });
  const row = await db.query("SELECT * FROM users WHERE name = ?", ["Alice"]);
  expect(row).toBeDefined();
});

// GOOD: Verifies through interface
test("createUser makes user retrievable", async () => {
  const user = await createUser({ name: "Alice" });
  const retrieved = await getUser(user.id);
  expect(retrieved.name).toBe("Alice");
});
```

## Go Examples

**Good test — table-driven, behavior-focused:**

```go
func TestCheckout(t *testing.T) {
    store := newInMemoryOrderStore()
    svc := NewCheckoutService(store, fixedPaymentGateway{})

    tests := []struct {
        name    string
        cart    Cart
        wantErr bool
    }{
        {"valid cart confirms order", Cart{Items: []Item{{SKU: "A", Qty: 1}}}, false},
        {"empty cart is rejected",   Cart{}, true},
    }

    for _, tc := range tests {
        t.Run(tc.name, func(t *testing.T) {
            order, err := svc.Checkout(context.Background(), tc.cart)
            if (err != nil) != tc.wantErr {
                t.Fatalf("Checkout() error = %v, wantErr %v", err, tc.wantErr)
            }
            if !tc.wantErr && order.Status != StatusConfirmed {
                t.Errorf("got status %q, want %q", order.Status, StatusConfirmed)
            }
        })
    }
}
```

**Bad test — coupled to internal struct, asserting on call count:**

```go
// BAD: reaches into internals and counts method calls
func TestCheckout_CallsGateway(t *testing.T) {
    spy := &spyGateway{}
    svc := &checkoutService{gateway: spy} // directly constructing internal struct
    svc.Checkout(context.Background(), validCart())
    if spy.chargeCallCount != 1 {
        t.Errorf("expected 1 charge call, got %d", spy.chargeCallCount)
    }
}
```

**Good vs bad — verify through interface, not storage:**

```go
// BAD: bypasses interface to inspect storage
func TestCreateUser_Bad(t *testing.T) {
    db := openTestDB(t)
    CreateUser(db, "Alice")
    var count int
    db.QueryRow("SELECT COUNT(*) FROM users WHERE name = ?", "Alice").Scan(&count)
    if count != 1 {
        t.Error("user not in DB")
    }
}

// GOOD: round-trips through the public interface
func TestCreateUser_Good(t *testing.T) {
    store := newInMemoryUserStore()
    svc := NewUserService(store)
    user, err := svc.CreateUser(context.Background(), "Alice")
    if err != nil {
        t.Fatal(err)
    }
    got, err := svc.GetUser(context.Background(), user.ID)
    if err != nil {
        t.Fatal(err)
    }
    if got.Name != "Alice" {
        t.Errorf("got name %q, want %q", got.Name, "Alice")
    }
}
```
